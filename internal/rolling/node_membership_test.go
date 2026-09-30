package rolling

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nodemember"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// memberDB is a db.DBTX that answers the node-membership lookup from members
// (or with lookErr), records every write by its sqlc name, and answers
// FailRollingUpdateNode with zero rows — "already terminal" — so failNode
// stops there instead of failing the whole job, which is not what these tests
// are about. Anything else fails the test.
type memberDB struct {
	t        *testing.T
	cluster  uuid.UUID
	members  []string
	lookErr  error
	mu       sync.Mutex
	asked    []string
	failures []string // the reason of each FailRollingUpdateNode
}

var sqlcQueryName = regexp.MustCompile(`-- name: (\w+)`)

func queryName(sql string) string {
	if m := sqlcQueryName.FindStringSubmatch(sql); m != nil {
		return m[1]
	}
	return sql
}

func (d *memberDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if name := queryName(sql); name != "FailRollingUpdateNode" {
		d.t.Errorf("unexpected write %s", name)
		return pgconn.CommandTag{}, errors.New("unexpected")
	}
	reason, _ := args[1].(string)
	d.mu.Lock()
	d.failures = append(d.failures, reason)
	d.mu.Unlock()
	return pgconn.NewCommandTag("UPDATE 0"), nil
}

func (d *memberDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.t.Errorf("unexpected query %s", queryName(sql))
	return nil, errors.New("unexpected")
}

func (d *memberDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if name := queryName(sql); name != "GetNodeByClusterAndName" {
		d.t.Errorf("unexpected query row %s", name)
		return rowErr{errors.New("unexpected")}
	}
	cluster, _ := args[0].(uuid.UUID)
	node, _ := args[1].(string)
	d.mu.Lock()
	d.asked = append(d.asked, cluster.String()+"/"+node)
	d.mu.Unlock()
	switch {
	case d.lookErr != nil:
		return rowErr{d.lookErr}
	case cluster == d.cluster && slices.Contains(d.members, node):
		return rowErr{}
	}
	return rowErr{pgx.ErrNoRows}
}

// rowErr is a row that scans nothing and answers err.
type rowErr struct{ err error }

func (r rowErr) Scan(...any) error { return r.err }

// TestNodeSteps_ANodeTheClusterDoesNotHoldIsNeverSentToProxmox: a rolling
// job's nodes are what the caller named at creation, and every step sends the
// name to Proxmox as /nodes/{node}/…, which pveproxy resolves and dials
// whatever it names. startNode (a pending node's first step), advanceNode
// (every automatic step after it) and ConfirmUpgrade (the step an operator
// confirms) send nothing for a node the cluster does not hold, and fail it
// with the reason; a failed lookup sends nothing and fails nothing, leaving
// the node for the next tick.
func TestNodeSteps_ANodeTheClusterDoesNotHoldIsNeverSentToProxmox(t *testing.T) {
	cluster := uuid.New()
	const stranger = "192.0.2.10"
	wantReason := "not one of this cluster's nodes; it was not contacted"

	type run func(t *testing.T, o *Orchestrator, client *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode)
	entries := map[string]run{
		"startNode, pending": func(_ *testing.T, o *Orchestrator, c *proxmox.Client, j db.RollingUpdateJob, n db.RollingUpdateNode) {
			n.Step = "pending"
			o.startNode(context.Background(), c, j, n)
		},
	}
	entries["ConfirmUpgrade, awaiting_upgrade with a reboot"] = func(t *testing.T, o *Orchestrator, _ *proxmox.Client, j db.RollingUpdateJob, n db.RollingUpdateNode) {
		// Confirming builds its own client — from the cluster row, which this
		// database fails the test for reading — and sends the node to the
		// drain check and the reboot. Its error is what the handler maps to
		// 409 (a stranger) or 500 (a failed lookup), so its type is pinned.
		n.Step = "awaiting_upgrade"
		j.RebootAfterUpdate = true
		err := o.ConfirmUpgrade(context.Background(), j, n)
		lookupFailed := strings.HasSuffix(t.Name(), "a_failed_lookup")
		if got := errors.As(err, new(*nodemember.LookupError)); got != lookupFailed ||
			(!lookupFailed && !errors.As(err, new(*nodemember.NotMemberError))) {
			t.Errorf("ConfirmUpgrade = %#v, want a *nodemember.%s", err,
				map[bool]string{true: "LookupError", false: "NotMemberError"}[lookupFailed])
		}
	}
	for _, step := range []string{"draining", "upgrading", "rebooting", "health_check", "restoring"} {
		entries["advanceNode, "+step] = func(_ *testing.T, o *Orchestrator, c *proxmox.Client, j db.RollingUpdateJob, n db.RollingUpdateNode) {
			n.Step = step
			o.advanceNode(context.Background(), c, j, n)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(entries)) {
		for _, tt := range []struct {
			label        string
			lookErr      error
			wantFailures []string
		}{
			{label: "a stranger", wantFailures: []string{wantReason}},
			{label: "a failed lookup", lookErr: errors.New("connection refused")},
		} {
			t.Run(name+": "+tt.label, func(t *testing.T) {
				var requests []string
				var mu sync.Mutex
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					requests = append(requests, r.Method+" "+r.URL.Path)
					mu.Unlock()
					http.Error(w, "unexpected", http.StatusInternalServerError)
				}))
				defer srv.Close()
				client, err := proxmox.NewClient(proxmox.ClientConfig{BaseURL: srv.URL, TokenID: "nexara@pve!api", TokenSecret: "secret"})
				if err != nil {
					t.Fatal(err)
				}

				d := &memberDB{t: t, cluster: cluster, members: []string{"pve-01"}, lookErr: tt.lookErr}
				o := NewOrchestrator(context.Background(), db.New(d), "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
				job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster}
				node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: stranger}

				entries[name](t, o, client, job, node)

				mu.Lock()
				defer mu.Unlock()
				if len(requests) != 0 {
					t.Errorf("sent %v to Proxmox for a node it could not confirm", requests)
				}
				if want := []string{cluster.String() + "/" + stranger}; !slices.Equal(d.asked, want) {
					t.Errorf("asked %v, want %v", d.asked, want)
				}
				if !slices.Equal(d.failures, tt.wantFailures) {
					t.Errorf("failed the node with %q, want %q", d.failures, tt.wantFailures)
				}
			})
		}
	}

	// A member passes the gate; nothing else is asked or written.
	d := &memberDB{t: t, cluster: cluster, members: []string{"pve-01"}}
	o := NewOrchestrator(context.Background(), db.New(d), "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	if err := o.requireDialableNode(context.Background(), db.RollingUpdateJob{ClusterID: cluster}, db.RollingUpdateNode{NodeName: "pve-01"}); err != nil {
		t.Errorf("a member was refused: %v", err)
	}
	if len(d.failures) != 0 {
		t.Errorf("a member failed the node: %q", d.failures)
	}
}
