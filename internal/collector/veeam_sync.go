package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/veeam"
)

// Timeouts for the two passes. The inventory pass fans out one restore-point
// listing per changed backup object, so it gets the longer budget; the session
// poll is one watermarked listing plus, at most, veeamUnfinishedSessionLimit
// single-session re-reads. Those only run while something is actually in
// flight, so the steady state is well inside the budget; the pass that can
// approach it is the first after an upgrade, when a backlog of frozen rows
// fills the cap. Overrunning is safe rather than merely tolerable — the
// deadline reaches the loop as ErrUnreachable, which abandons the batch, and
// the remaining rows are picked up next pass.
const (
	veeamInventoryTimeout = 5 * time.Minute
	veeamSessionsTimeout  = 90 * time.Second
)

// veeamSessionOverlap is subtracted from the stored watermark before it is used
// as createdAfterFilter.
//
// A session's creationTime is set by the VBR server, so one created moments
// before a poll can carry a timestamp fractionally earlier than the newest row
// that poll stored. Re-asking for a few minutes of already-seen sessions costs
// one page of idempotent upserts and removes that class of gap entirely.
const veeamSessionOverlap = 10 * time.Minute

// veeamSessionPageLimit bounds one session poll. With typeFilter applied
// server-side a healthy server returns a handful of rows per minute; this only
// bites on a first sync or after a long outage, where taking the newest 500
// and letting the next tick continue is the right shape.
const veeamSessionPageLimit = 500

// veeamUnfinishedSessionLimit bounds how many stored in-flight runs one pass
// re-reads by id. Fifty is far more than a real server has in flight at once,
// so the cap only ever bites on a backlog left by a long outage — which then
// converges over a handful of passes rather than in one burst of requests.
const veeamUnfinishedSessionLimit = 50

// veeamRestorePointWorkers bounds how many restore-point listings are in
// flight at once.
//
// The fan-out is one request per backup object per pass, and "one row per
// (guest × backup)" means a 500-guest install with daily, weekly and monthly
// jobs is ~1500 objects. Serially at 200ms each that is 300s — the entire pass
// budget — after which the context dies and every remaining object silently
// gets no restore points at all. Six at a time keeps that inside the budget
// without turning Nexara into a load generator against someone's VBR.
const veeamRestorePointWorkers = 6

// veeamSweepGraceSeconds is the window a stale-row sweep allows before
// deleting. Generous relative to the pass cadence: a row missed by one pass is
// almost always a blip, and re-creating a deleted object mints a fresh
// surrogate id that anything keyed on it would lose.
const veeamSweepGraceSeconds = 900

// Backoff for a server that keeps failing. Bounded so a server that comes back
// is picked up within half an hour without an operator touching anything.
const (
	veeamBackoffBase = 1 * time.Minute
	veeamBackoffMax  = 30 * time.Minute
)

// Pass names, used for backoff keys and for deciding which pass owns the
// server row's sync status.
const (
	veeamPassInventory = "inventory"
	veeamPassSessions  = "sessions"
)

// VeeamSyncQueries is the database surface the Veeam sync needs.
//
// Deliberately its own interface rather than an extension of SyncQueries: none
// of this shares a credential, a client or a table with the Proxmox sync, and
// folding ~18 methods into that interface would force every existing test stub
// to grow implementations it never calls.
type VeeamSyncQueries interface {
	ListActiveVeeamServers(ctx context.Context) ([]db.VeeamServer, error)
	SetVeeamServerSyncCompleted(ctx context.Context, arg db.SetVeeamServerSyncCompletedParams) error
	SetVeeamServerSyncError(ctx context.Context, arg db.SetVeeamServerSyncErrorParams) error

	UpsertVeeamPlatform(ctx context.Context, arg db.UpsertVeeamPlatformParams) error
	CorrelateVeeamBackupObjects(ctx context.Context, veeamServerID uuid.UUID) (int64, error)

	ListVeeamRepositoriesByServer(ctx context.Context, veeamServerID uuid.UUID) ([]db.VeeamRepository, error)
	UpsertVeeamRepository(ctx context.Context, arg db.UpsertVeeamRepositoryParams) error
	DeleteStaleVeeamRepositories(ctx context.Context, arg db.DeleteStaleVeeamRepositoriesParams) error
	InsertVeeamRepositoryMetric(ctx context.Context, arg db.InsertVeeamRepositoryMetricParams) error

	CountVeeamJobsByServer(ctx context.Context, veeamServerID uuid.UUID) (int64, error)
	UpsertVeeamJob(ctx context.Context, arg db.UpsertVeeamJobParams) error
	DeleteStaleVeeamJobs(ctx context.Context, arg db.DeleteStaleVeeamJobsParams) error
	DeriveVeeamJobPlatforms(ctx context.Context, veeamServerID uuid.UUID) error

	UpsertVeeamInfrastructure(ctx context.Context, arg db.UpsertVeeamInfrastructureParams) error
	DeleteStaleVeeamInfrastructure(ctx context.Context, arg db.DeleteStaleVeeamInfrastructureParams) error
	ResolveVeeamInfrastructureGuests(ctx context.Context, veeamServerID uuid.UUID) (int64, error)

	ListVeeamBackupObjectsByServer(ctx context.Context, veeamServerID uuid.UUID) ([]db.VeeamBackupObject, error)
	UpsertVeeamBackupObject(ctx context.Context, arg db.UpsertVeeamBackupObjectParams) (db.VeeamBackupObject, error)
	DeleteStaleVeeamBackupObjects(ctx context.Context, arg db.DeleteStaleVeeamBackupObjectsParams) error

	UpsertVeeamRestorePoint(ctx context.Context, arg db.UpsertVeeamRestorePointParams) error
	PruneVeeamRestorePoints(ctx context.Context, arg db.PruneVeeamRestorePointsParams) error

	GetVeeamSessionWatermark(ctx context.Context, veeamServerID uuid.UUID) (time.Time, error)
	SetVeeamSessionsSyncedAt(ctx context.Context, id uuid.UUID) error
	UpsertVeeamSession(ctx context.Context, arg db.UpsertVeeamSessionParams) error
	PruneVeeamSessions(ctx context.Context, arg db.PruneVeeamSessionsParams) error
	ListVeeamUnfinishedSessions(ctx context.Context, arg db.ListVeeamUnfinishedSessionsParams) ([]uuid.UUID, error)
	DeleteVeeamSession(ctx context.Context, arg db.DeleteVeeamSessionParams) error
}

// VeeamClientFactory builds a client for one server. Swapped in tests.
type VeeamClientFactory func(cfg veeam.Config) (VeeamClient, error)

// VeeamClient is the slice of *veeam.Client the sync uses.
type VeeamClient interface {
	Repositories(ctx context.Context) ([]veeam.Repository, error)
	JobStates(ctx context.Context) ([]veeam.JobState, error)
	ProxyStates(ctx context.Context) ([]veeam.ProxyState, error)
	ManagedServers(ctx context.Context) ([]veeam.ManagedServer, error)
	BackupObjects(ctx context.Context) ([]veeam.BackupObject, error)
	RestorePointsForObject(ctx context.Context, objectID string) ([]veeam.RestorePoint, error)
	Sessions(ctx context.Context, since time.Time, limit int) ([]veeam.Session, error)
	Session(ctx context.Context, sessionID string) (*veeam.Session, error)
	License(ctx context.Context) (*veeam.License, error)
	Logout(ctx context.Context) error
}

// DefaultVeeamClientFactory builds a real client.
func DefaultVeeamClientFactory(cfg veeam.Config) (VeeamClient, error) {
	return veeam.New(cfg)
}

// VeeamSyncConfig carries the retention windows the sweeps use.
type VeeamSyncConfig struct {
	// SyncInterval is how often the inventory pass runs. Only used to size the
	// sweep grace window — a fixed grace would be shorter than a single
	// interval on a slow cadence, so one missed pass would delete rows the
	// window exists to protect.
	SyncInterval time.Duration
	// RestorePointRetention bounds how long a restore point Veeam has STOPPED
	// reporting is kept.
	RestorePointRetention time.Duration
	// SessionRetention bounds the history window of job runs Nexara mirrors.
	SessionRetention time.Duration
}

// VeeamSyncer polls registered Veeam servers into the local inventory.
type VeeamSyncer struct {
	queries       VeeamSyncQueries
	encryptionKey string
	logger        *slog.Logger
	clientFactory VeeamClientFactory
	eventPub      eventPublisher
	cfg           VeeamSyncConfig

	inventoryInFlight atomic.Bool
	sessionsInFlight  atomic.Bool

	backoffMu sync.Mutex
	backoff   map[string]veeamBackoff
	// passNote is the current problem (or warning) per (server, pass), and is
	// what publishStatus recomputes the server row from.
	passNote map[string]string
}

// veeamBackoff is one server's failure state.
type veeamBackoff struct {
	failures int
	nextTry  time.Time
}

// NewVeeamSyncer creates a Veeam inventory syncer.
func NewVeeamSyncer(queries VeeamSyncQueries, encryptionKey string, cfg VeeamSyncConfig, logger *slog.Logger) *VeeamSyncer {
	if cfg.RestorePointRetention <= 0 {
		cfg.RestorePointRetention = 30 * 24 * time.Hour
	}
	if cfg.SessionRetention <= 0 {
		cfg.SessionRetention = 30 * 24 * time.Hour
	}
	if cfg.SyncInterval <= 0 {
		cfg.SyncInterval = 5 * time.Minute
	}
	return &VeeamSyncer{
		queries:       queries,
		encryptionKey: encryptionKey,
		logger:        logger,
		clientFactory: DefaultVeeamClientFactory,
		cfg:           cfg,
		backoff:       map[string]veeamBackoff{},
		passNote:      map[string]string{},
	}
}

// InventoryTrigger is a coalescing request for an out-of-band inventory pass.
//
// It deliberately does NOT hang off VeeamSyncer. The syncer is built inside the
// collector goroutine, while the API handler that fires the trigger is built
// before that goroutine starts — handing the handler a syncer-owned channel
// would mean writing the handler's field from the collector goroutine while
// request goroutines read it. Creating the trigger up front and passing it to
// both sides keeps the wiring race-free.
type InventoryTrigger struct{ ch chan struct{} }

// NewInventoryTrigger returns a trigger with a one-slot buffer.
func NewInventoryTrigger() *InventoryTrigger {
	return &InventoryTrigger{ch: make(chan struct{}, 1)}
}

// TriggerInventory asks for an inventory pass as soon as the loop is free.
//
// Registering a server is the case this exists for: without it a new server
// shows nothing until the next tick, which is VeeamSyncInterval (5 minutes by
// default) of empty tables. The immediate pass the inventory loop runs at
// startup only covers servers that already existed, not one added while Nexara
// is already running.
//
// Non-blocking, so N rapid registrations coalesce into at most one extra pass:
// a dropped trigger means one is already queued, and that pass will pick up
// every server anyway. A trigger raised while a pass is in flight is still
// honoured — the loop is the serializer, and it reads the buffered nudge the
// moment it returns to its select.
//
// Nil-safe: a nil trigger is the "no collector wired" case, not an error.
func (t *InventoryTrigger) TriggerInventory() {
	if t == nil {
		return
	}
	select {
	case t.ch <- struct{}{}:
	default:
	}
}

// C is the channel an inventory loop selects on alongside its ticker. A nil
// trigger yields a nil channel, which blocks forever in a select — exactly the
// "nobody can trigger this" behaviour, with no branch at the call site.
func (t *InventoryTrigger) C() <-chan struct{} {
	if t == nil {
		return nil
	}
	return t.ch
}

// sweepGraceSeconds is the grace window in seconds: the floor, or three sync
// intervals, whichever is larger. Three so a row survives two consecutive
// missed passes before anything deletes it.
func (v *VeeamSyncer) sweepGraceSeconds() int32 {
	grace := max(veeamSweepGraceSeconds, int(3*v.cfg.SyncInterval.Seconds()))
	return int32(min(grace, math.MaxInt32)) //nolint:gosec // clamped to int32 on the line above
}

// SetEventPublisher attaches an event publisher so the SPA learns about
// changes without polling.
func (v *VeeamSyncer) SetEventPublisher(pub eventPublisher) { v.eventPub = pub }

// SyncInventory runs one inventory pass over every enabled server:
// repositories and their capacity sample, job states, backup objects and the
// restore points of any object whose point count moved.
//
// Re-entrancy-guarded: a tick that arrives while a pass is still running is
// skipped, not queued. A slow VBR must not build a backlog of passes that all
// hit it at once when it recovers.
func (v *VeeamSyncer) SyncInventory(ctx context.Context) {
	if !v.inventoryInFlight.CompareAndSwap(false, true) {
		return
	}
	defer v.inventoryInFlight.Store(false)

	v.forEachServer(ctx, veeamPassInventory, veeamInventoryTimeout, v.syncServerInventory)
}

// SyncSessions runs one session poll over every enabled server. Cheap and
// frequent: a watermarked, type-filtered listing per server, followed by a
// targeted re-read of the unfinished runs that listing can no longer reach
// (see reconcileUnfinishedSessions).
func (v *VeeamSyncer) SyncSessions(ctx context.Context) {
	if !v.sessionsInFlight.CompareAndSwap(false, true) {
		return
	}
	defer v.sessionsInFlight.Store(false)

	v.forEachServer(ctx, veeamPassSessions, veeamSessionsTimeout, v.syncServerSessions)
}

// forEachServer runs fn against every enabled server concurrently, with each
// server isolated from the others: one server's panic, timeout or auth failure
// cannot stop the rest, and only that server's row records the error.
func (v *VeeamSyncer) forEachServer(
	ctx context.Context,
	pass string,
	timeout time.Duration,
	fn func(context.Context, db.VeeamServer, VeeamClient) (passResult, error),
) {
	servers, err := v.queries.ListActiveVeeamServers(ctx)
	if err != nil {
		v.logger.Warn("veeam sync: failed to list servers", "pass", pass, "error", err)
		return
	}

	var wg sync.WaitGroup
	for _, server := range servers {
		if !v.shouldAttempt(server.ID, pass) {
			continue
		}
		wg.Add(1)
		go func(server db.VeeamServer) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					v.logger.Error("veeam sync panicked",
						"pass", pass, "veeam_server_id", server.ID, "panic", r)
					v.recordFailure(ctx, server, pass, fmt.Errorf("internal error during %s sync", pass))
				}
			}()

			passCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			client, err := v.clientFor(server)
			if err != nil {
				v.recordFailure(ctx, server, pass, err)
				return
			}
			// The token is released rather than left to expire on its own:
			// a poll every 60s against a 15-minute token would otherwise keep
			// a rolling handful of live sessions open on the VBR server.
			//
			// On its OWN short context, not passCtx: when fn hits the pass
			// deadline passCtx is already done, and a logout on a dead context
			// fails instantly — precisely in the hung-server case where the
			// leaked token matters most.
			defer func() {
				logoutCtx, logoutCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
				defer logoutCancel()
				_ = client.Logout(logoutCtx)
			}()

			res, err := fn(passCtx, server, client)
			if err != nil {
				v.recordFailure(ctx, server, pass, err)
				return
			}
			v.recordSuccess(ctx, server, pass, res)
		}(server)
	}
	wg.Wait()
}

// clientFor decrypts a server's credential and builds its client.
func (v *VeeamSyncer) clientFor(server db.VeeamServer) (VeeamClient, error) {
	password, err := crypto.Decrypt(server.PasswordEncrypted, v.encryptionKey)
	if err != nil {
		// Deliberately vague: this string reaches last_sync_error, which is
		// rendered in the UI. "decryption failed" is all an operator can act
		// on, and the underlying error adds nothing but detail about the
		// ciphertext.
		return nil, errors.New("stored credential could not be decrypted")
	}
	return v.clientFactory(veeam.Config{
		BaseURL:        server.BaseUrl,
		Username:       server.Username,
		Password:       password,
		APIRevision:    server.ApiRevision,
		TLSFingerprint: server.TlsFingerprint,
		VerifyTLS:      server.VerifyTls,
		Timeout:        veeamSessionsTimeout,
	})
}

// syncServerInventory converges one server's repositories, jobs and backup
// objects.
//
// Each section sweeps its own stale rows only if its own fetch succeeded, and
// the whole pass reports the first failure. A section that could not be read
// leaves its existing rows alone rather than deleting them — a Veeam outage
// must not look like "everything was deleted".
func (v *VeeamSyncer) syncServerInventory(ctx context.Context, server db.VeeamServer, client VeeamClient) (passResult, error) {
	var res passResult
	platforms := map[string]struct{}{}

	if err := v.syncRepositories(ctx, server, client, &res); err != nil {
		return res, err
	}
	if err := v.syncJobs(ctx, server, client, &res); err != nil {
		return res, err
	}
	if err := v.syncBackupObjects(ctx, server, client, &res, platforms); err != nil {
		return res, err
	}
	// Deliberately not error-returning. A failure here must not discard the
	// repositories, jobs and backup objects this pass has ALREADY written, nor
	// skip the platform recording and correlation that follow — the least
	// valuable listing on the server would otherwise throw away the most
	// valuable enrichment of an otherwise good pass, and put the server into
	// backoff besides. It raises a warning instead, which reaches
	// last_sync_error alongside a fresh last_sync_at: "synced, with a caveat".
	v.syncInfrastructure(ctx, server, client, &res)

	v.recordPlatforms(ctx, server, client, platforms)

	// Sticky by construction — see the query. Run here as well as after the
	// session poll so a job learns its platform on the first inventory pass
	// after sessions have landed.
	if err := v.queries.DeriveVeeamJobPlatforms(ctx, server.ID); err != nil {
		v.logger.Warn("veeam sync: deriving job platforms failed",
			"veeam_server_id", server.ID, "error", err)
	}

	// Above the prune's early return, deliberately. Correlation reads the
	// object set as it stands — including one a refused sweep chose to keep —
	// and has nothing to do with restore-point retention, so inheriting that
	// guard would strand a freshly mapped platform uncorrelated for as long
	// as a VBR server took to finish loading its catalog.
	v.correlate(ctx, server)

	// The prune is gated on the SAME verdict as the sweeps, not merely on the
	// pass not erroring. A pass that refused to sweep because the catalog came
	// back empty touched no restore points either — so pruning on last_seen_at
	// would age out every point for every guest on the clock alone, which with
	// a short retention window deletes the entire recovery history while the
	// guard congratulates itself for keeping the objects.
	if res.refusedSweep {
		return res, nil
	}
	if err := v.queries.PruneVeeamRestorePoints(ctx, db.PruneVeeamRestorePointsParams{
		VeeamServerID: server.ID,
		// Clamped, not asserted: an absurd retention would otherwise wrap
		// negative, turn the cutoff into a future date, and delete every
		// restore point Veeam still holds.
		GraceSeconds: int32(min(v.cfg.RestorePointRetention.Seconds(), math.MaxInt32)), //nolint:gosec // clamped on this line
	}); err != nil {
		v.logger.Warn("veeam sync: pruning restore points failed",
			"veeam_server_id", server.ID, "error", err)
	}

	v.publishChange(ctx, "inventory_synced")
	return res, nil
}

// syncInfrastructure records the guests that belong to the Veeam deployment
// itself — worker appliances and, when it runs on the cluster it protects, the
// VBR server.
//
// They are not backup targets, and a coverage report that lists them as
// unprotected VMs is one operators stop reading. Four of the lab's nineteen
// naive alarms are these.
//
// Both listings are CONFIGURATION, unlike the backup catalog, so the stale
// sweep here is an ordinary grace-windowed one with no empty-listing refusal —
// see DeleteStaleVeeamInfrastructure for why that trade goes the other way for
// this table.
func (v *VeeamSyncer) syncInfrastructure(ctx context.Context, server db.VeeamServer, client VeeamClient, res *passResult) {
	// swept names the roles whose listing was read successfully this pass, and
	// is the ONLY thing the stale sweep is allowed to delete. A section that
	// could not be read must leave its rows alone: three consecutive failures
	// of a listing a caller is not authorized for would otherwise age its rows
	// out of existence and put those guests back into the coverage report as
	// false alarms, with a clean last_sync_at and nothing to explain it.
	var swept []string

	// ⚠️ /proxies/states, not /proxies. Verified against a live 13.1 server:
	// the plain listing has no Proxmox rows at all, while /states has every
	// worker — the same trap job states carry, and documented nowhere.
	proxies, err := client.ProxyStates(ctx)
	if err != nil {
		v.logger.Warn("veeam sync: backup proxies unreadable",
			"veeam_server_id", server.ID, "error", err)
		res.warn("Veeam's own worker appliances could not be listed, so guests belonging to them may be reported as unprotected.")
	} else {
		swept = append(swept, veeamRoleWorker)
		for _, p := range proxies {
			if !p.IsProxmox() {
				continue
			}
			ref, ok := parseUUID(p.ID)
			if !ok {
				v.logger.Warn("veeam sync: proxy has an unparseable id",
					"veeam_server_id", server.ID, "name", p.Name)
				continue
			}
			if uErr := v.queries.UpsertVeeamInfrastructure(ctx, db.UpsertVeeamInfrastructureParams{
				VeeamServerID: server.ID,
				VeeamRef:      ref,
				Role:          veeamRoleWorker,
				Name:          p.Name,
				HostName:      p.HostName,
				IsDisabled:    p.IsDisabled,
				IsOnline:      p.IsOnline,
			}); uErr != nil {
				v.logger.Warn("veeam sync: upserting a worker appliance failed",
					"veeam_server_id", server.ID, "name", p.Name, "error", uErr)
				// The listing was read; one row failing to store is not a
				// reason to let the sweep delete the rest.
			}
		}
	}

	// The VBR server itself, which matters only when it runs as a guest on the
	// cluster it protects — the lab's does. Commonly unreadable by a caller
	// whose rights stop at the backup catalog, hence Debug rather than a
	// warning an operator cannot act on.
	if servers, mErr := client.ManagedServers(ctx); mErr != nil {
		v.logger.Debug("veeam sync: managed servers unreadable",
			"veeam_server_id", server.ID, "error", mErr)
	} else {
		swept = append(swept, veeamRoleBackupServer)
		for _, m := range servers {
			if !m.IsBackupServer {
				continue
			}
			ref, ok := parseUUID(m.ID)
			if !ok {
				// Logged, not silent: without this the backup-server half of
				// the feature would do nothing and look exactly like a
				// deployment whose VBR simply is not a guest on the cluster.
				v.logger.Warn("veeam sync: backup server has an unparseable id",
					"veeam_server_id", server.ID, "name", m.Name)
				continue
			}
			if uErr := v.queries.UpsertVeeamInfrastructure(ctx, db.UpsertVeeamInfrastructureParams{
				VeeamServerID: server.ID,
				VeeamRef:      ref,
				Role:          veeamRoleBackupServer,
				// The managedServers FQDN, which is what matches a guest
				// name. serverInfo.name is the short form and matches none.
				Name: m.Name,
				// A managed server is reachable or it is not; "disabled" is a
				// proxy concept and does not apply.
				IsOnline: m.Status == veeamManagedServerAvailable,
			}); uErr != nil {
				v.logger.Warn("veeam sync: upserting the backup server failed",
					"veeam_server_id", server.ID, "name", m.Name, "error", uErr)
			}
		}
	}

	if len(swept) == 0 {
		return
	}
	if err := v.queries.DeleteStaleVeeamInfrastructure(ctx, db.DeleteStaleVeeamInfrastructureParams{
		VeeamServerID: server.ID,
		// Non-nil by construction — the guard above returns first. pgx encodes
		// a nil slice as SQL NULL and role = ANY(NULL) is NULL, so a nil here
		// would silently delete nothing rather than everything, but relying on
		// that would make the guard look optional.
		Roles:        swept,
		GraceSeconds: v.sweepGraceSeconds(),
	}); err != nil {
		v.logger.Warn("veeam sync: pruning stale Veeam infrastructure failed",
			"veeam_server_id", server.ID, "error", err)
	}

	// Resolving these rows to guests happens in correlate(), not here: it
	// searches the clusters this server has MAPPED platforms on, and
	// recordPlatforms has not run yet at this point in the pass. Doing it
	// here would resolve nothing on the very first sync of a new server.
}

// Roles a Veeam-owned guest can have, matching the CHECK on
// veeam_infrastructure.role.
const (
	veeamRoleWorker       = "worker"
	veeamRoleBackupServer = "backup_server"
)

// veeamManagedServerAvailable is EManagedServerStatus's healthy value.
const veeamManagedServerAvailable = "Available"

// correlate resolves this server's backup objects, and Veeam's own guests, to
// Nexara guests.
//
// Runs after recordPlatforms, so a platform discovered by this very pass — and
// auto-mapped by its own INSERT when the install has a single cluster — is
// correlated in the same tick rather than the next one.
//
// It cannot fail the pass. The inventory is correct without it: correlation is
// an enrichment, and refusing to record a successful sync because a follow-up
// UPDATE failed would put the server into backoff and stop collecting the
// very data the correlation decorates.
//
// The guest side of the join is filled by the collector's own SMBIOS pass,
// which only visits clusters a platform is already mapped to. A freshly mapped
// platform therefore converges over two ticks: everything reads as unresolved
// until that pass has recorded what each guest's SMBIOS uuid is (or that it
// has none), which is the honest answer — the alternative, name-matching
// guests nothing is known about, reports orphaned backups as live protection.
func (v *VeeamSyncer) correlate(ctx context.Context, server db.VeeamServer) {
	if changed, err := v.queries.CorrelateVeeamBackupObjects(ctx, server.ID); err != nil {
		v.logger.Warn("veeam sync: correlating backup objects failed",
			"veeam_server_id", server.ID, "error", err)
	} else if changed > 0 {
		v.logger.Info("veeam sync: guest correlation updated",
			"veeam_server_id", server.ID, "objects", changed)
	}

	// Run unconditionally rather than only when a row was just written: a
	// guest RENAMED out from under a resolved row has to lose its resolution,
	// and nothing about the Veeam-side listing changes when that happens.
	if changed, err := v.queries.ResolveVeeamInfrastructureGuests(ctx, server.ID); err != nil {
		v.logger.Warn("veeam sync: resolving Veeam infrastructure guests failed",
			"veeam_server_id", server.ID, "error", err)
	} else if changed > 0 {
		v.logger.Info("veeam sync: Veeam infrastructure guests resolved",
			"veeam_server_id", server.ID, "rows", changed)
	}
}

// passResult is what one pass wants to say about itself beyond succeeding or
// failing.
type passResult struct {
	// warnings are conditions worth showing an operator on a pass that
	// otherwise completed. They reach last_sync_error alongside a fresh
	// last_sync_at, which is the honest rendering of "synced, with a caveat".
	warnings []string
	// refusedSweep records that at least one section declined to prune. It
	// suppresses the restore-point prune for the same reason.
	refusedSweep bool
}

func (r *passResult) warn(format string, args ...any) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
}

// note joins the warnings into the single line the server row carries.
func (r passResult) note() string {
	return truncateSyncError(strings.Join(r.warnings, " "))
}

func (v *VeeamSyncer) syncRepositories(ctx context.Context, server db.VeeamServer, client VeeamClient, res *passResult) error {
	repos, err := client.Repositories(ctx)
	if err != nil {
		return fmt.Errorf("repositories: %w", err)
	}

	existing, err := v.queries.ListVeeamRepositoriesByServer(ctx, server.ID)
	if err != nil {
		return fmt.Errorf("list existing repositories: %w", err)
	}
	var stored int

	for _, r := range repos {
		veeamID, ok := parseUUID(r.ID)
		if !ok {
			v.logger.Warn("veeam sync: repository has an unparseable id",
				"veeam_server_id", server.ID, "name", r.Name)
			continue
		}
		if err := v.queries.UpsertVeeamRepository(ctx, db.UpsertVeeamRepositoryParams{
			VeeamServerID: server.ID,
			VeeamID:       veeamID,
			Name:          r.Name,
			RepoType:      r.Type,
			HostName:      r.HostName,
			Path:          r.Path,
			CapacityBytes: r.CapacityBytes(),
			FreeBytes:     r.FreeBytes(),
			UsedBytes:     r.UsedBytes(),
			IsOnline:      r.IsOnline,
			IsOutOfDate:   r.IsOutOfDate,
		}); err != nil {
			return fmt.Errorf("upsert repository %s: %w", r.Name, err)
		}
		stored++

		// One capacity sample per pass, feeding the hypertable behind the
		// usage chart and the future repo-full alert. Skipped for a
		// repository reporting no capacity at all, which would otherwise
		// plot a flat zero line for a target Veeam could not measure.
		if r.CapacityBytes() == 0 {
			continue
		}
		if err := v.queries.InsertVeeamRepositoryMetric(ctx, db.InsertVeeamRepositoryMetricParams{
			VeeamServerID:     server.ID,
			RepositoryVeeamID: veeamID,
			CapacityBytes:     r.CapacityBytes(),
			FreeBytes:         r.FreeBytes(),
			UsedBytes:         r.UsedBytes(),
		}); err != nil {
			v.logger.Warn("veeam sync: repository metric insert failed",
				"veeam_server_id", server.ID, "repository", r.Name, "error", err)
		}
	}

	if !v.shouldSweep(server, res, "repositories", stored, len(existing)) {
		return nil
	}
	return v.queries.DeleteStaleVeeamRepositories(ctx, db.DeleteStaleVeeamRepositoriesParams{
		VeeamServerID: server.ID,
		GraceSeconds:  v.sweepGraceSeconds(),
	})
}

func (v *VeeamSyncer) syncJobs(ctx context.Context, server db.VeeamServer, client VeeamClient, res *passResult) error {
	states, err := client.JobStates(ctx)
	if err != nil {
		return fmt.Errorf("job states: %w", err)
	}

	existing, err := v.queries.CountVeeamJobsByServer(ctx, server.ID)
	if err != nil {
		return fmt.Errorf("count existing jobs: %w", err)
	}
	var stored int

	for _, j := range states {
		// Proxmox jobs only. A VBR server almost always runs vSphere, agent
		// and file jobs too, and Nexara has nothing to say about those.
		if !j.IsProxmox() {
			continue
		}
		veeamID, ok := parseUUID(j.ID)
		if !ok {
			v.logger.Warn("veeam sync: job has an unparseable id",
				"veeam_server_id", server.ID, "name", j.Name)
			continue
		}

		if err := v.queries.UpsertVeeamJob(ctx, db.UpsertVeeamJobParams{
			VeeamServerID:     server.ID,
			VeeamID:           veeamID,
			Name:              j.Name,
			JobType:           j.Type,
			Workload:          j.Workload,
			Description:       j.Description,
			Status:            j.Status,
			LastResult:        j.LastResult,
			LastRun:           optionalTimestamp(j.LastRun.Or()),
			NextRun:           optionalTimestamp(j.NextRun.Or()),
			NextRunPolicy:     j.NextRunPolicy,
			RepositoryVeeamID: optionalUUID(j.RepositoryID),
			RepositoryName:    j.RepositoryName,
			ObjectsCount:      int32(j.ObjectsCount), //nolint:gosec // a job's object count is bounded by the guests on one platform
			LastSessionID:     optionalUUID(j.SessionID),
			ProgressPercent:   int32(j.ProgressPercent), //nolint:gosec // a percentage
			Bottleneck:        j.SessionProgress.Bottleneck,
			Duration:          j.SessionProgress.Duration,
			ProcessingRate:    j.SessionProgress.ProcessingRate,
			ProcessedSize:     j.SessionProgress.ProcessedSize,
			ReadSize:          j.SessionProgress.ReadSize,
			TransferredSize:   j.SessionProgress.TransferredSize,
		}); err != nil {
			return fmt.Errorf("upsert job %s: %w", j.Name, err)
		}
		stored++
	}

	if !v.shouldSweep(server, res, "backup jobs", stored, int(existing)) {
		return nil
	}
	return v.queries.DeleteStaleVeeamJobs(ctx, db.DeleteStaleVeeamJobsParams{
		VeeamServerID: server.ID,
		GraceSeconds:  v.sweepGraceSeconds(),
	})
}

// syncBackupObjects converges backup objects and their restore points.
//
// GET /backupObjects returns one row per (guest × backup), and the SAME object
// id repeats across them — verified on a live server, where 27 rows carried 18
// distinct ids. The id identifies the guest within Veeam; the backup is what
// differs. So the rows are folded to one per guest before anything is stored,
// which is also the grain guest correlation needs.
//
// Folding is not just deduplication: restorePointsCount is PER BACKUP, and
// /backupObjects/{id}/restorePoints returns the guest's points across all of
// them. Taking any single row's count would understate it — measured on the
// lab, a guest whose three rows read [3, 17, 9] has 29 restore points, and
// keeping "9" made the stored count disagree with the points beside it.
//
// Every guest's restore points are re-fetched every pass. Skipping objects
// whose count had not moved looked like a free optimisation and was not: a job
// that keeps N restore points saturates at N and adds one and deletes one on
// every run thereafter, so the count never moves again and the skip becomes
// permanent — Nexara would freeze on the day the ceiling was hit, never learn
// another restore point, and keep the deleted ones alive forever. That is the
// steady state for every count-retention job, not an edge case.
func (v *VeeamSyncer) syncBackupObjects(
	ctx context.Context,
	server db.VeeamServer,
	client VeeamClient,
	res *passResult,
	platforms map[string]struct{},
) error {
	objects, err := client.BackupObjects(ctx)
	if err != nil {
		return fmt.Errorf("backup objects: %w", err)
	}

	existing, err := v.queries.ListVeeamBackupObjectsByServer(ctx, server.ID)
	if err != nil {
		return fmt.Errorf("list existing backup objects: %w", err)
	}

	guests := foldBackupObjects(objects, platforms, func(name string) {
		v.logger.Warn("veeam sync: backup object has an unparseable id",
			"veeam_server_id", server.ID, "name", name)
	})

	pending := make([]veeamPointFetch, 0, len(guests))
	for _, g := range guests {
		row, err := v.queries.UpsertVeeamBackupObject(ctx, db.UpsertVeeamBackupObjectParams{
			VeeamServerID:      server.ID,
			VeeamObjectID:      g.objectID,
			SmbiosUuid:         g.object.ObjectID,
			PlatformID:         optionalUUID(g.object.PlatformID),
			Name:               g.object.Name,
			ObjectType:         g.object.Type,
			BackupRef:          optionalUUID(g.object.BackupID),
			RestorePointsCount: g.restorePoints,
			SizeBytes:          g.object.Size,
			LastRunFailed:      g.lastRunFailed,
		})
		if err != nil {
			return fmt.Errorf("upsert backup object %s: %w", g.object.Name, err)
		}
		pending = append(pending, veeamPointFetch{object: g.object, rowID: row.ID})
	}

	if err := v.fetchRestorePoints(ctx, server, client, res, pending); err != nil {
		return err
	}

	if !v.shouldSweep(server, res, "backup objects", len(guests), len(existing)) {
		return nil
	}
	return v.queries.DeleteStaleVeeamBackupObjects(ctx, db.DeleteStaleVeeamBackupObjectsParams{
		VeeamServerID: server.ID,
		GraceSeconds:  v.sweepGraceSeconds(),
	})
}

// foldedObject is one guest, folded from every backup it appears in.
type foldedObject struct {
	object        veeam.BackupObject
	objectID      uuid.UUID
	restorePoints int32
	lastRunFailed bool
}

// foldBackupObjects folds the (guest × backup) listing to one entry per guest,
// preserving listing order so the result is stable across passes.
//
// Counts are summed because each row's is per-backup; size is taken as-is
// because it is the guest's own size and is identical across its rows
// (verified on live data); lastRunFailed is OR'd, since a failure in any
// backup of a guest is worth surfacing.
func foldBackupObjects(
	objects []veeam.BackupObject,
	platforms map[string]struct{},
	onBadID func(name string),
) []foldedObject {
	index := make(map[uuid.UUID]int, len(objects))
	folded := make([]foldedObject, 0, len(objects))

	for _, o := range objects {
		if !o.IsProxmox() {
			continue
		}
		objectID, ok := parseUUID(o.ID)
		if !ok {
			onBadID(o.Name)
			continue
		}
		if o.PlatformID != "" {
			platforms[o.PlatformID] = struct{}{}
		}

		count := int32(min(o.RestorePointsCount, math.MaxInt32)) //nolint:gosec // clamped on this line
		if at, seen := index[objectID]; seen {
			folded[at].restorePoints += count
			folded[at].lastRunFailed = folded[at].lastRunFailed || o.LastRunFailed
			continue
		}
		index[objectID] = len(folded)
		folded = append(folded, foldedObject{
			object:        o,
			objectID:      objectID,
			restorePoints: count,
			lastRunFailed: o.LastRunFailed,
		})
	}
	return folded
}

// shouldSweep decides whether a section's stale-row deletion may run.
//
// A successful read that returned NOTHING, for a section that previously had
// rows, is refused. The VBR REST service answers before its backup service has
// finished loading its catalog after a restart, so an empty listing is a real
// and transient state — and acting on it would delete every backup object,
// cascade away every restore point, and then report the server healthy. For a
// backup-monitoring product that renders as total protection loss, which is a
// far worse failure than carrying stale rows until the next pass.
//
// The refusal is recorded on the pass rather than written straight to the
// server row: writing it here meant the pass's own success cleared it moments
// later, so the operator saw "last synced: just now, no error" over stale data.
func (v *VeeamSyncer) shouldSweep(server db.VeeamServer, res *passResult, section string, fetched, previous int) bool {
	if fetched > 0 || previous == 0 {
		return true
	}
	v.logger.Warn("veeam sync: refusing to prune on an empty listing",
		"veeam_server_id", server.ID, "section", section, "previously_stored", previous)
	res.refusedSweep = true
	res.warn("Veeam returned no %s while %d are on record; keeping the existing rows, which may be out of date.",
		section, previous)
	return false
}

// veeamPointFetch pairs a Veeam backup object with the local row its restore
// points belong to.
type veeamPointFetch struct {
	object veeam.BackupObject
	rowID  uuid.UUID
}

// fetchRestorePoints runs the per-object listings over a bounded worker pool.
//
// One object failing does not invalidate the rest of the inventory: it is
// logged and the pass continues, so a single bad guest cannot stall every
// other guest's data. Its existing points keep their last_seen_at from the
// previous pass and only age out after a full retention window of continuous
// failure.
//
// A dead context is different — it means the pass ran out of budget, and every
// remaining object would fail identically. That stops the fan-out and fails
// the pass, so the operator sees a timeout rather than a silently partial
// inventory that reports success.
func (v *VeeamSyncer) fetchRestorePoints(
	ctx context.Context,
	server db.VeeamServer,
	client VeeamClient,
	res *passResult,
	pending []veeamPointFetch,
) error {
	if len(pending) == 0 {
		return nil
	}

	workers := min(veeamRestorePointWorkers, len(pending))
	queue := make(chan veeamPointFetch)
	var wg sync.WaitGroup
	var failed atomic.Int64

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range queue {
				if ctx.Err() != nil {
					continue // drain; the producer already stopped feeding
				}
				if err := v.syncRestorePoints(ctx, server, client, item.object, item.rowID); err != nil {
					failed.Add(1)
					v.logger.Warn("veeam sync: restore points failed",
						"veeam_server_id", server.ID, "object", item.object.Name, "error", err)
				}
			}
		}()
	}

	var budgetExhausted bool
	for _, item := range pending {
		if ctx.Err() != nil {
			budgetExhausted = true
			break
		}
		queue <- item
	}
	close(queue)
	wg.Wait()

	if budgetExhausted {
		return fmt.Errorf("restore points: ran out of time after %d of %d objects", len(pending)-int(failed.Load()), len(pending))
	}
	if n := failed.Load(); n > 0 {
		res.warn("Restore points could not be read for %d of %d backup objects.", n, len(pending))
	}
	return nil
}

func (v *VeeamSyncer) syncRestorePoints(
	ctx context.Context,
	server db.VeeamServer,
	client VeeamClient,
	object veeam.BackupObject,
	backupObjectID uuid.UUID,
) error {
	points, err := client.RestorePointsForObject(ctx, object.ID)
	if err != nil {
		return err
	}

	for _, p := range points {
		pointID, ok := parseUUID(p.ID)
		if !ok {
			continue
		}
		// Same guard the session loop applies: creation_time is NOT NULL, and
		// a zero time would store as year 1, sort last in every "newest point"
		// query, and quietly make the guest look unprotected.
		if p.CreationTime.IsZero() {
			v.logger.Warn("veeam sync: restore point has no parseable creation time",
				"veeam_server_id", server.ID, "object", object.Name, "point", p.ID)
			continue
		}
		if err := v.queries.UpsertVeeamRestorePoint(ctx, db.UpsertVeeamRestorePointParams{
			VeeamServerID:  server.ID,
			BackupObjectID: backupObjectID,
			VeeamID:        pointID,
			Name:           p.Name,
			PointType:      p.Type,
			MalwareStatus:  p.MalwareStatus,
			GuestOsFamily:  p.GuestOSFamily,
			CreationTime:   p.CreationTime.Time,
			SizeBytes:      p.OriginalSize,
			BackupID:       optionalUUID(p.BackupID),
			SessionID:      optionalUUID(p.SessionID),
			BackupFileID:   optionalUUID(p.BackupFileID),
			SupportsFlr:    p.SupportsFLR(),
		}); err != nil {
			return fmt.Errorf("upsert restore point: %w", err)
		}
	}
	return nil
}

// syncServerSessions polls job runs newer than the stored watermark.
func (v *VeeamSyncer) syncServerSessions(ctx context.Context, server db.VeeamServer, client VeeamClient) (passResult, error) {
	var res passResult

	since, err := v.sessionWatermark(ctx, server)
	if err != nil {
		return res, err
	}

	sessions, err := client.Sessions(ctx, since, veeamSessionPageLimit)
	if err != nil {
		return res, fmt.Errorf("sessions: %w", err)
	}

	platforms := map[string]struct{}{}
	// What this poll already brought up to date, so the reconcile below does
	// not spend a request re-reading a row that is current as of a moment ago.
	refreshed := map[uuid.UUID]struct{}{}
	// OLDEST first, deliberately: the API returns newest-first, and storing in
	// that order means a failure partway through has already committed the
	// NEWEST rows — which advances the watermark past the ones that failed, so
	// the next poll's createdAfterFilter skips them and they are never fetched
	// again. Walking backwards makes whatever gets stored a contiguous run
	// from the oldest, so the watermark only ever advances over rows that
	// actually landed and a retry resumes exactly where this pass stopped.
	for i := len(sessions) - 1; i >= 0; i-- {
		s := sessions[i]
		// typeFilter narrows server-side to PlatformBackupJob, which covers
		// every platform Veeam calls one. platformName is what makes it ours.
		if !s.IsProxmoxBackup() {
			continue
		}
		sessionID, ok := parseUUID(s.ID)
		if !ok {
			continue
		}
		// A session with no usable creation time cannot be stored: the column
		// is NOT NULL, the zero time would be pruned on this very pass, and it
		// would corrupt the watermark. Warn rather than swallow — a whole
		// server's worth of these means the API changed its timestamp format.
		if s.CreationTime.IsZero() {
			v.logger.Warn("veeam sync: session has no parseable creation time",
				"veeam_server_id", server.ID, "session", s.Name)
			continue
		}
		if s.PlatformID != "" {
			platforms[s.PlatformID] = struct{}{}
		}

		if err := v.queries.UpsertVeeamSession(ctx, veeamSessionParams(server.ID, sessionID, s)); err != nil {
			return res, fmt.Errorf("upsert session %s: %w", s.Name, err)
		}
		refreshed[sessionID] = struct{}{}
	}

	// Catches up the in-flight runs the window above can no longer reach.
	v.reconcileUnfinishedSessions(ctx, server, client, refreshed)

	// Sessions are the only bridge from a job to its platform, so this runs
	// immediately after they land rather than waiting for the slower pass.
	if err := v.queries.DeriveVeeamJobPlatforms(ctx, server.ID); err != nil {
		v.logger.Warn("veeam sync: deriving job platforms failed",
			"veeam_server_id", server.ID, "error", err)
	}

	if err := v.queries.PruneVeeamSessions(ctx, db.PruneVeeamSessionsParams{
		VeeamServerID: server.ID,
		CreationTime:  time.Now().Add(-v.cfg.SessionRetention),
	}); err != nil {
		v.logger.Warn("veeam sync: pruning sessions failed",
			"veeam_server_id", server.ID, "error", err)
	}

	// Stamped only once everything above has landed, so a pass that failed
	// part-way leaves the server on the retention floor and re-reads the
	// window next time rather than narrowing onto a partial result.
	if err := v.queries.SetVeeamSessionsSyncedAt(ctx, server.ID); err != nil {
		v.logger.Warn("veeam sync: stamping the session poll failed",
			"veeam_server_id", server.ID, "error", err)
	}

	if len(platforms) > 0 {
		v.publishChange(ctx, "sessions_synced")
	}
	return res, nil
}

// veeamSessionParams maps one API session onto the upsert.
//
// Shared by the poll and the reconcile so the two can never write a session
// differently — a reconcile that dropped a field would leave the row worse
// than the poll left it, which is the opposite of the point.
func veeamSessionParams(serverID, sessionID uuid.UUID, s veeam.Session) db.UpsertVeeamSessionParams {
	return db.UpsertVeeamSessionParams{
		VeeamServerID:   serverID,
		VeeamID:         sessionID,
		JobVeeamID:      optionalUUID(s.JobID),
		Name:            s.Name,
		SessionType:     s.SessionType,
		PlatformName:    s.PlatformName,
		PlatformID:      optionalUUID(s.PlatformID),
		State:           s.State,
		Result:          s.Result.Result,
		ResultMessage:   s.Result.Message,
		IsCanceled:      s.Result.IsCanceled,
		Algorithm:       s.Algorithm,
		Bottleneck:      s.Progress.Bottleneck,
		Duration:        s.Progress.Duration,
		ProcessingRate:  s.Progress.ProcessingRate,
		ProcessedSize:   s.Progress.ProcessedSize,
		ReadSize:        s.Progress.ReadSize,
		TransferredSize: s.Progress.TransferredSize,
		ProgressPercent: int32(s.ProgressPercent), //nolint:gosec // a percentage
		CreationTime:    s.CreationTime.Time,
		EndTime:         optionalTimestamp(s.EndTime.Or()),
		InitiatedBy:     s.InitiatedBy,
	}
}

// reconcileUnfinishedSessions re-reads the stored runs that have not reached a
// terminal state, so their final state actually lands.
//
// The poll above is a createdAfterFilter window over CREATION time, but a
// session's STATE keeps changing long after it is created — so the window can
// never re-observe a row it has already scrolled past. A run still in flight
// when a newer run pushes the watermark beyond it is frozen at whatever state
// the last poll saw ("Working"), with end_time NULL, permanently: no later
// poll asks for it again.
//
// That is not a cosmetic staleness. ListVeeamJobsByServer derives a job's live
// run from exactly these rows, so one frozen session paints its job "Running"
// for good — hours after Veeam itself has gone back to reporting the job as
// Stopped, and with the Stop button offered in place of Run.
//
// Failures here are logged, never fatal: the poll itself succeeded, and losing
// its watermark advance over one unreadable session would re-fetch the whole
// window every pass for as long as that session stayed unreadable.
func (v *VeeamSyncer) reconcileUnfinishedSessions(
	ctx context.Context,
	server db.VeeamServer,
	client VeeamClient,
	refreshed map[uuid.UUID]struct{},
) {
	unfinished, err := v.queries.ListVeeamUnfinishedSessions(ctx, db.ListVeeamUnfinishedSessionsParams{
		VeeamServerID: server.ID,
		Limit:         veeamUnfinishedSessionLimit,
	})
	if err != nil {
		v.logger.Warn("veeam sync: listing unfinished sessions failed",
			"veeam_server_id", server.ID, "error", err)
		return
	}

	for _, sessionID := range unfinished {
		if _, done := refreshed[sessionID]; done {
			continue
		}

		s, err := client.Session(ctx, sessionID.String())
		if err != nil {
			// ErrSessionNotFound is VBR saying this run is gone. It can never
			// reach a terminal state now, so keeping the row pins its job to
			// "Running" forever — the mirror drops it instead.
			//
			// The sentinel, never a 404 read off an *APIError: a failed token
			// grant surfaces the TOKEN endpoint's error verbatim, so a
			// restarting VBR answering 404 there would otherwise look exactly
			// like this and delete every row in the batch — including runs
			// genuinely in flight, along with the nexara_stopped provenance
			// that suppresses a false veeam_job_failed for them.
			if errors.Is(err, veeam.ErrSessionNotFound) {
				if err := v.queries.DeleteVeeamSession(ctx, db.DeleteVeeamSessionParams{
					VeeamServerID: server.ID,
					VeeamID:       sessionID,
				}); err != nil {
					v.logger.Warn("veeam sync: dropping a vanished session failed",
						"veeam_server_id", server.ID, "session", sessionID, "error", err)
				}
				continue
			}

			v.logger.Warn("veeam sync: re-reading an unfinished session failed",
				"veeam_server_id", server.ID, "session", sessionID, "error", err)

			// A broken credential or an unreachable server is a fact about the
			// CLIENT, not about this row, so every remaining id would fail the
			// same way — abandon the batch rather than spend fifty doomed
			// round-trips on it. Anything else is per-session (a 500 on one
			// run) and must not block the rows behind it: with the newest
			// first, one permanently-failing run would starve every older one.
			if errors.Is(err, veeam.ErrAuthFailed) || errors.Is(err, veeam.ErrUnreachable) {
				return
			}
			continue
		}

		// creation_time is NOT NULL, feeds the watermark, and would be pruned
		// on this very pass if it were zero — the same guard the poll applies.
		if s.CreationTime.IsZero() {
			v.logger.Warn("veeam sync: re-read session has no parseable creation time",
				"veeam_server_id", server.ID, "session", sessionID)
			continue
		}

		if err := v.queries.UpsertVeeamSession(ctx, veeamSessionParams(server.ID, sessionID, *s)); err != nil {
			v.logger.Warn("veeam sync: storing a re-read session failed",
				"veeam_server_id", server.ID, "session", sessionID, "error", err)
		}
	}
}

// sessionWatermark returns the createdAfterFilter for the next poll.
//
// The stored maximum minus an overlap, or — on an empty table — the start of
// the retention window, so a first sync against a server with tens of
// thousands of historical sessions pulls only what Nexara intends to keep.
func (v *VeeamSyncer) sessionWatermark(ctx context.Context, server db.VeeamServer) (time.Time, error) {
	// "First sync" is a fact about the POLL, not about the table.
	//
	// Job control writes a session row of its own the moment an operator
	// starts or stops a job. On a server whose session pass has not yet
	// succeeded — it is in backoff while the inventory pass succeeded and
	// listed the jobs, so the UI offers the button — that single row would
	// otherwise become the watermark, anchoring `since` to minutes ago instead
	// of the retention window. A watermark only ever moves forward, so the
	// backfill would never happen and the server's history would be
	// permanently short. Gating on sessions_synced_at is what tells the two
	// apart; the emptiness test below stays as a second line of defence.
	if !server.SessionsSyncedAt.Valid {
		return time.Now().Add(-v.cfg.SessionRetention), nil
	}

	mark, err := v.queries.GetVeeamSessionWatermark(ctx, server.ID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && mark.IsZero()) {
		return time.Now().Add(-v.cfg.SessionRetention), nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("session watermark: %w", err)
	}
	return mark.Add(-veeamSessionOverlap), nil
}

// recordPlatforms upserts the Veeam platforms this pass observed, labelling
// them from the licence workload list.
//
// The licence is the only place a human-readable name for a Proxmox connection
// appears anywhere in the API. A failure to read it is not a sync failure: the
// platform rows still matter, they are just unlabelled until the next pass.
func (v *VeeamSyncer) recordPlatforms(ctx context.Context, server db.VeeamServer, client VeeamClient, platforms map[string]struct{}) {
	if len(platforms) == 0 {
		return
	}

	label := ""
	if lic, err := client.License(ctx); err == nil {
		if clusters := lic.ProxmoxClusters(); len(clusters) == 1 {
			// Only when the licence describes exactly one Proxmox cluster. With
			// several there is no way to tell which platformId is which, and a
			// wrong label is worse than none — an operator confirms the
			// cluster mapping anyway.
			label = clusters[0].Name
		}
	} else {
		v.logger.Debug("veeam sync: licence read failed while labelling platforms",
			"veeam_server_id", server.ID, "error", err)
	}

	for raw := range platforms {
		platformID, ok := parseUUID(raw)
		if !ok {
			continue
		}
		if err := v.queries.UpsertVeeamPlatform(ctx, db.UpsertVeeamPlatformParams{
			VeeamServerID: server.ID,
			PlatformID:    platformID,
			DisplayName:   label,
		}); err != nil {
			v.logger.Warn("veeam sync: upserting platform failed",
				"veeam_server_id", server.ID, "error", err)
		}
	}
}

// backoffKey scopes failure state to one server AND one pass.
//
// Sharing it between the passes made the cheap 60s session poll clear the
// expensive inventory pass's backoff every minute, so a server whose
// /backupObjects endpoint was broken never actually backed off.
func backoffKey(serverID uuid.UUID, pass string) string {
	return serverID.String() + ":" + pass
}

// shouldAttempt reports whether a server is past its backoff window for a pass.
func (v *VeeamSyncer) shouldAttempt(serverID uuid.UUID, pass string) bool {
	v.backoffMu.Lock()
	defer v.backoffMu.Unlock()
	state, ok := v.backoff[backoffKey(serverID, pass)]
	return !ok || !time.Now().Before(state.nextTry)
}

// recordFailure extends the pass's backoff and republishes the server's
// status.
func (v *VeeamSyncer) recordFailure(ctx context.Context, server db.VeeamServer, pass string, cause error) {
	key := backoffKey(server.ID, pass)

	v.backoffMu.Lock()
	state := v.backoff[key]
	state.failures++
	delay := veeamBackoffBase << min(state.failures-1, 8)
	if delay > veeamBackoffMax || delay <= 0 {
		delay = veeamBackoffMax
	}
	state.nextTry = time.Now().Add(delay)
	v.backoff[key] = state
	failures := state.failures
	// The message reaches the UI verbatim, so it is bounded here — nothing
	// upstream limits the length of an error a remote server can produce.
	v.passNote[key] = truncateSyncError(cause.Error())
	v.backoffMu.Unlock()

	v.logger.Warn("veeam sync failed",
		"pass", pass, "veeam_server_id", server.ID, "consecutive_failures", failures,
		"retry_in", delay, "error", cause)

	v.publishStatus(ctx, server, pass, false)
}

// recordSuccess clears the pass's backoff and republishes the server's status,
// carrying any warning the pass raised.
func (v *VeeamSyncer) recordSuccess(ctx context.Context, server db.VeeamServer, pass string, res passResult) {
	key := backoffKey(server.ID, pass)

	v.backoffMu.Lock()
	delete(v.backoff, key)
	if note := res.note(); note != "" {
		v.passNote[key] = note
	} else {
		delete(v.passNote, key)
	}
	v.backoffMu.Unlock()

	v.publishStatus(ctx, server, pass, true)
}

// publishStatus writes the server row from the CURRENT state of every pass.
//
// Two passes share one last_sync_error column, and each of the obvious rules
// for that is wrong on its own. Letting both write it made the healthy 60s
// session poll clear a persistent inventory failure every minute, so the UI
// error flashed on and off; letting only the inventory pass write it made a
// permanently failing session poll completely invisible. Recomputing from both
// passes' current notes gives neither failure mode: a pass can only ever clear
// its OWN note, and whatever is still wrong stays on the row.
//
// last_sync_at is stamped only by a successful inventory pass — it means "the
// inventory was last read successfully at", which a session poll cannot speak
// to.
func (v *VeeamSyncer) publishStatus(ctx context.Context, server db.VeeamServer, pass string, succeeded bool) {
	v.backoffMu.Lock()
	notes := make([]string, 0, 2)
	// Inventory first: it is the more comprehensive pass, so its problem is
	// the one to lead with when both are unhappy.
	for _, p := range []string{veeamPassInventory, veeamPassSessions} {
		if note := v.passNote[backoffKey(server.ID, p)]; note != "" {
			notes = append(notes, note)
		}
	}
	v.backoffMu.Unlock()

	combined := truncateSyncError(strings.Join(notes, " | "))

	if succeeded && pass == veeamPassInventory {
		if err := v.queries.SetVeeamServerSyncCompleted(ctx, db.SetVeeamServerSyncCompletedParams{
			ID:            server.ID,
			LastSyncError: combined,
		}); err != nil {
			v.logger.Warn("veeam sync: recording sync completion failed",
				"veeam_server_id", server.ID, "error", err)
		}
		return
	}

	if err := v.queries.SetVeeamServerSyncError(ctx, db.SetVeeamServerSyncErrorParams{
		ID:            server.ID,
		LastSyncError: combined,
	}); err != nil {
		v.logger.Warn("veeam sync: recording sync status failed",
			"veeam_server_id", server.ID, "error", err)
	}
}

func (v *VeeamSyncer) publishChange(ctx context.Context, action string) {
	if v.eventPub == nil {
		return
	}
	v.eventPub.SystemEvent(ctx, events.KindVeeamChange, action)
}

// maxSyncErrorLen bounds last_sync_error. The column is TEXT and the value can
// originate from a remote server.
const maxSyncErrorLen = 500

func truncateSyncError(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= maxSyncErrorLen {
		return s
	}
	return string(r[:maxSyncErrorLen]) + "…"
}

// parseUUID converts an API-supplied identifier, reporting whether it was
// well-formed. Veeam identifies everything with UUIDs; a value that is not one
// means the row is not what this code thinks it is, and skipping it beats
// storing a zero UUID that would silently collide with every other bad row.
func parseUUID(s string) (uuid.UUID, bool) {
	if s == "" {
		return uuid.Nil, false
	}
	parsed, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return parsed, true
}

// optionalUUID maps an absent, malformed or all-zero identifier to SQL NULL.
//
// The zero UUID matters: Veeam uses "00000000-0000-0000-0000-000000000000"
// for "no related session", and storing that literally would create a fake
// identifier that every other unrelated row also matches.
func optionalUUID(s string) pgtype.UUID {
	parsed, ok := parseUUID(s)
	if !ok || parsed == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

// optionalTimestamp maps the zero time to SQL NULL, so "never ran" is stored
// as absent rather than as the year 1.
func optionalTimestamp(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}
