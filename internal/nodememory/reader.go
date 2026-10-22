package nodememory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/singleflight"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/rolling"
	"github.com/bigjakk/nexara/internal/safeconv"
	sshpkg "github.com/bigjakk/nexara/internal/ssh"
)

// A read's outcome, as node_memory_inventory.status stores it.
const (
	StatusOK = "ok"
	// StatusUnavailable means nothing was dialed: the cluster has no SSH
	// credentials, the node no known address, or its host key is not pinned.
	StatusUnavailable = "unavailable"
	// StatusError means the node was tried and the read failed.
	StatusError = "error"
)

const (
	// readTimeout bounds the SSH round trip: connecting and the session setup,
	// each capped at 30 s inside internal/ssh, then dmidecode, which answers in
	// well under a second.
	readTimeout = 45 * time.Second
	// writeTimeout is the store's own budget after the read, so a read that
	// used all of readTimeout still records its failure.
	writeTimeout = 10 * time.Second
	// readCooldown is how soon a node may be read on request again after a
	// read that reached for it: each is a root login, and a rejected login
	// repeated fast enough gets Nexara's address banned by fail2ban.
	readCooldown = time.Minute
)

// ErrReadTooSoon refuses a Read now within readCooldown of the node's last
// read that dialed it.
var ErrReadTooSoon = errors.New("this node was read less than a minute ago")

// Runner runs one command on a node over its cluster's SSH credentials:
// rolling.RunNodeCommandLimited with MaxOutput in production.
type Runner func(ctx context.Context, clusterID uuid.UUID, nodeName, command string) (*sshpkg.ExecResult, error)

// Store is the part of the generated queries the reader uses.
type Store interface {
	ListNodeMemoryReadState(ctx context.Context) ([]db.ListNodeMemoryReadStateRow, error)
	UpsertNodeMemoryRead(ctx context.Context, arg db.UpsertNodeMemoryReadParams) error
	RecordNodeMemoryFailure(ctx context.Context, arg db.RecordNodeMemoryFailureParams) error
	InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) error
}

// Reader reads nodes' memory layouts and stores each outcome. One Reader
// serves both the daily pass and a node page's Read now, so its per-node
// singleflight keeps the two from opening two root sessions on one node.
type Reader struct {
	run      Runner
	store    Store
	interval time.Duration
	now      func() time.Time
	logger   *slog.Logger
	sf       singleflight.Group

	mu     sync.Mutex
	dialed map[uuid.UUID]time.Time // each node's last read that reached for it
}

// NewReader builds a Reader. interval is how old a node's last read may get
// before the daily pass repeats it; 0 turns the pass off, and Read now still
// works.
func NewReader(run Runner, store Store, interval time.Duration, logger *slog.Logger) *Reader {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reader{run: run, store: store, interval: interval, now: time.Now, logger: logger, dialed: map[uuid.UUID]time.Time{}}
}

// Interval is the daily pass's re-read interval; 0 means the pass is off.
func (r *Reader) Interval() time.Duration { return r.interval }

// ReadNode reads one node now, on a person's request, and stores the outcome,
// a failure included. It refuses with ErrReadTooSoon within readCooldown of a
// read that dialed the node. Otherwise its error is only a failure to store:
// why a read failed is the stored reason.
func (r *Reader) ReadNode(ctx context.Context, clusterID, nodeID uuid.UUID, nodeName string) error {
	r.mu.Lock()
	last, ok := r.dialed[nodeID]
	r.mu.Unlock()
	if ok && r.now().Sub(last) < readCooldown {
		return ErrReadTooSoon
	}
	_, err := r.shared(ctx, clusterID, nodeID, nodeName)
	return err
}

// RunDue reads, one at a time, every online node of an active cluster that is
// due: never tried, read longer ago than the interval, or stopped short of
// dialing last time. Re-checking a node without credentials, address or pinned
// key costs no login, so credentials added later are picked up by the next
// pass. A read that reached the node and failed waits a whole interval, so a
// rejected login is retried once a day rather than every hour. Each read that
// dialed is audited as the system actor.
func (r *Reader) RunDue(ctx context.Context) {
	if r.interval <= 0 {
		return
	}
	nodes, err := r.store.ListNodeMemoryReadState(ctx)
	if err != nil {
		r.logger.Warn("node memory: list nodes", "error", err)
		return
	}
	now := r.now()
	for _, n := range nodes {
		if ctx.Err() != nil {
			return
		}
		if n.Status != "online" || !due(n, now, r.interval) {
			continue
		}
		status, err := r.shared(ctx, n.ClusterID, n.NodeID, n.Name)
		if err != nil {
			r.logger.Warn("node memory: store the read", "node", n.Name, "error", err)
		}
		// A read that dialed is audited even when storing it failed: a node can
		// make its own read unstorable, and its login must still be traceable.
		if status != "" && status != StatusUnavailable {
			if err != nil {
				status = "store_failed"
			}
			r.audit(ctx, n.ClusterID, n.Name, status)
		}
	}
}

func due(n db.ListNodeMemoryReadStateRow, now time.Time, interval time.Duration) bool {
	if !n.AttemptedAt.Valid || n.ReadStatus.String == StatusUnavailable {
		return true
	}
	return now.Sub(n.AttemptedAt.Time) >= interval
}

// shared runs at most one read per node at a time. A caller whose own ctx ends
// stops waiting; the read runs on under the ctx of the caller that started it.
func (r *Reader) shared(ctx context.Context, clusterID, nodeID uuid.UUID, nodeName string) (string, error) {
	ch := r.sf.DoChan(nodeID.String(), func() (v any, err error) {
		// singleflight re-panics a DoChan panic where nothing can recover it,
		// so a node whose output panicked the read would take Nexara down.
		defer func() {
			if p := recover(); p != nil {
				r.logger.Error("node memory read panicked", "node", nodeName, "panic", p)
				v, err = StatusError, fmt.Errorf("node memory read panicked: %v", p)
				// Recorded like any failed read, so the node waits a whole
				// interval and a cooldown instead of a root login every pass.
				at := r.now()
				r.mu.Lock()
				r.dialed[nodeID] = at
				r.mu.Unlock()
				wctx, cancel := context.WithTimeout(ctx, writeTimeout)
				defer cancel()
				_ = r.store.RecordNodeMemoryFailure(wctx, db.RecordNodeMemoryFailureParams{
					NodeID: nodeID, Status: StatusError, Reason: "The memory layout could not be read.", AttemptedAt: at,
				})
			}
		}()
		return r.read(ctx, clusterID, nodeID, nodeName)
	})
	select {
	case res := <-ch:
		status, _ := res.Val.(string)
		return status, res.Err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// read runs dmidecode on the node and stores the outcome. The write has its
// own budget under the caller's ctx: a read that used all of readTimeout must
// still record its failure, and a pass whose leadership ended still cannot
// land a late result.
func (r *Reader) read(ctx context.Context, clusterID, nodeID uuid.UUID, nodeName string) (string, error) {
	at := r.now()
	runCtx, cancelRun := context.WithTimeout(ctx, readTimeout)
	res, runErr := r.run(runCtx, clusterID, nodeName, Command)
	cancelRun()
	inv, status, reason := outcome(res, runErr)
	if status != StatusUnavailable {
		r.mu.Lock()
		r.dialed[nodeID] = at
		r.mu.Unlock()
	}

	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	switch {
	case status == StatusOK:
		modules, err := json.Marshal(inv.Modules)
		if err != nil {
			return status, fmt.Errorf("encode modules: %w", err)
		}
		return status, r.store.UpsertNodeMemoryRead(ctx, db.UpsertNodeMemoryReadParams{
			NodeID:           nodeID,
			ReadAt:           at,
			InstalledBytes:   inv.InstalledBytes(),
			SlotsUsed:        safeconv.Int32(inv.SlotsUsed()),
			SlotsTotal:       safeconv.Int32(len(inv.Modules)),
			MaxCapacityBytes: inv.MaxCapacityBytes,
			Ecc:              inv.ECC,
			Modules:          modules,
		})
	case runErr != nil && status == StatusError:
		// The error carries addresses and fingerprints: logged, never stored.
		// Truncated because a node's SSH disconnect message rides inside it.
		r.logger.Warn("node memory read failed", "cluster_id", clusterID, "node", nodeName, "error", truncate(runErr.Error(), 512))
	case status == StatusError:
		r.logger.Warn("node memory read failed", "cluster_id", clusterID, "node", nodeName,
			"exit_code", res.ExitCode, "stderr", truncate(strings.TrimSpace(res.Stderr), 512))
	}
	return status, r.store.RecordNodeMemoryFailure(ctx, db.RecordNodeMemoryFailureParams{
		NodeID: nodeID, Status: status, Reason: reason, AttemptedAt: at,
	})
}

// audit records a background read that reached for a node, as the system
// actor: a daily root login is something an operator has to be able to trace.
// A read that dialed nothing is not recorded; it repeats hourly and touches no
// node.
func (r *Reader) audit(ctx context.Context, clusterID uuid.UUID, nodeName, status string) {
	details, _ := json.Marshal(map[string]string{"status": status, "trigger": "daily"})
	if err := r.store.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: clusterID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "read_memory_modules",
		Details:      details,
	}); err != nil {
		r.logger.Warn("node memory: audit the read", "node", nodeName, "error", err)
	}
}

// outcome turns one run into what to store, with a reason fit to show.
func outcome(res *sshpkg.ExecResult, err error) (inv Inventory, status, reason string) {
	if err != nil {
		if errors.Is(err, sshpkg.ErrOutputTooLarge) {
			return Inventory{}, StatusError, "dmidecode printed far more than a memory layout; nothing was stored."
		}
		why, classified := rolling.UnavailableReason(err, "the memory layout")
		if classified {
			return Inventory{}, StatusUnavailable, why
		}
		return Inventory{}, StatusError, why
	}
	switch {
	case res.ExitCode == 127 || strings.Contains(res.Stderr, "command not found"):
		return Inventory{}, StatusError, "dmidecode is not installed on this node."
	case strings.Contains(res.Stderr, "Permission denied"):
		return Inventory{}, StatusError, "Reading the memory layout needs root: this cluster's SSH user cannot read the node's DMI tables."
	case res.ExitCode != 0:
		return Inventory{}, StatusError, "dmidecode failed on this node."
	}
	inv, err = Parse(res.Stdout)
	if err != nil {
		return Inventory{}, StatusError, "dmidecode's output could not be read as a memory layout."
	}
	if len(inv.Modules) == 0 {
		return inv, StatusError, "The node's firmware lists no memory devices."
	}
	return inv, StatusOK, ""
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
