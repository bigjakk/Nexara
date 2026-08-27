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
// poll is a single watermarked listing and should never come close to its own.
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

	ListVeeamRepositoriesByServer(ctx context.Context, veeamServerID uuid.UUID) ([]db.VeeamRepository, error)
	UpsertVeeamRepository(ctx context.Context, arg db.UpsertVeeamRepositoryParams) error
	DeleteStaleVeeamRepositories(ctx context.Context, arg db.DeleteStaleVeeamRepositoriesParams) error
	InsertVeeamRepositoryMetric(ctx context.Context, arg db.InsertVeeamRepositoryMetricParams) error

	ListVeeamJobsByServer(ctx context.Context, veeamServerID uuid.UUID) ([]db.VeeamJob, error)
	UpsertVeeamJob(ctx context.Context, arg db.UpsertVeeamJobParams) error
	DeleteStaleVeeamJobs(ctx context.Context, arg db.DeleteStaleVeeamJobsParams) error
	DeriveVeeamJobPlatforms(ctx context.Context, veeamServerID uuid.UUID) error

	ListVeeamBackupObjectsByServer(ctx context.Context, veeamServerID uuid.UUID) ([]db.VeeamBackupObject, error)
	UpsertVeeamBackupObject(ctx context.Context, arg db.UpsertVeeamBackupObjectParams) (db.VeeamBackupObject, error)
	DeleteStaleVeeamBackupObjects(ctx context.Context, arg db.DeleteStaleVeeamBackupObjectsParams) error

	UpsertVeeamRestorePoint(ctx context.Context, arg db.UpsertVeeamRestorePointParams) error
	PruneVeeamRestorePoints(ctx context.Context, arg db.PruneVeeamRestorePointsParams) error

	GetVeeamSessionWatermark(ctx context.Context, veeamServerID uuid.UUID) (time.Time, error)
	UpsertVeeamSession(ctx context.Context, arg db.UpsertVeeamSessionParams) error
	PruneVeeamSessions(ctx context.Context, arg db.PruneVeeamSessionsParams) error
}

// VeeamClientFactory builds a client for one server. Swapped in tests.
type VeeamClientFactory func(cfg veeam.Config) (VeeamClient, error)

// VeeamClient is the slice of *veeam.Client the sync uses.
type VeeamClient interface {
	Repositories(ctx context.Context) ([]veeam.Repository, error)
	JobStates(ctx context.Context) ([]veeam.JobState, error)
	BackupObjects(ctx context.Context) ([]veeam.BackupObject, error)
	RestorePointsForObject(ctx context.Context, objectID string) ([]veeam.RestorePoint, error)
	Sessions(ctx context.Context, since time.Time, limit int) ([]veeam.Session, error)
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
// frequent: a single watermarked, type-filtered listing per server.
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

	v.recordPlatforms(ctx, server, client, platforms)

	// Sticky by construction — see the query. Run here as well as after the
	// session poll so a job learns its platform on the first inventory pass
	// after sessions have landed.
	if err := v.queries.DeriveVeeamJobPlatforms(ctx, server.ID); err != nil {
		v.logger.Warn("veeam sync: deriving job platforms failed",
			"veeam_server_id", server.ID, "error", err)
	}

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

	existing, err := v.queries.ListVeeamJobsByServer(ctx, server.ID)
	if err != nil {
		return fmt.Errorf("list existing jobs: %w", err)
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

	if !v.shouldSweep(server, res, "backup jobs", stored, len(existing)) {
		return nil
	}
	return v.queries.DeleteStaleVeeamJobs(ctx, db.DeleteStaleVeeamJobsParams{
		VeeamServerID: server.ID,
		GraceSeconds:  v.sweepGraceSeconds(),
	})
}

// syncBackupObjects converges backup objects and their restore points.
//
// Every Proxmox object's restore points are re-fetched every pass. An earlier
// version skipped objects whose restorePointsCount had not moved, which looked
// like a free optimisation and was not: a job configured to keep N restore
// points saturates at N and stays there, adding one and deleting one on every
// run. The count never moves again, so the skip becomes permanent — Nexara
// would freeze on the day the ceiling was hit, never learn another restore
// point, and keep the deleted ones alive forever. That is the steady state for
// every count-retention job, not an edge case, and it would quietly corrupt
// every RPO and coverage figure downstream.
//
// The cost is one listing per backup object per pass. On a large install that
// is a few requests a second at the default five-minute cadence, and
// VEEAM_SYNC_INTERVAL exists for operators who want it lower.
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

	// Restore-point listings are fanned out over a bounded worker pool rather
	// than fetched inline: serially, a large install's object count exceeds
	// the whole pass budget and everything past the cut-off silently gets no
	// restore points.
	var pending []veeamPointFetch

	var stored int
	for _, o := range objects {
		if !o.IsProxmox() {
			continue
		}
		objectID, ok := parseUUID(o.ID)
		if !ok {
			v.logger.Warn("veeam sync: backup object has an unparseable id",
				"veeam_server_id", server.ID, "name", o.Name)
			continue
		}
		if o.PlatformID != "" {
			platforms[o.PlatformID] = struct{}{}
		}

		row, err := v.queries.UpsertVeeamBackupObject(ctx, db.UpsertVeeamBackupObjectParams{
			VeeamServerID:      server.ID,
			VeeamObjectID:      objectID,
			SmbiosUuid:         o.ObjectID,
			PlatformID:         optionalUUID(o.PlatformID),
			Name:               o.Name,
			ObjectType:         o.Type,
			BackupRef:          optionalUUID(o.BackupID),
			RestorePointsCount: int32(o.RestorePointsCount), //nolint:gosec // bounded by a job's retention policy
			SizeBytes:          o.Size,
			LastRunFailed:      o.LastRunFailed,
		})
		if err != nil {
			return fmt.Errorf("upsert backup object %s: %w", o.Name, err)
		}
		stored++
		pending = append(pending, veeamPointFetch{object: o, rowID: row.ID})
	}

	if err := v.fetchRestorePoints(ctx, server, client, res, pending); err != nil {
		return err
	}

	if !v.shouldSweep(server, res, "backup objects", stored, len(existing)) {
		return nil
	}
	return v.queries.DeleteStaleVeeamBackupObjects(ctx, db.DeleteStaleVeeamBackupObjectsParams{
		VeeamServerID: server.ID,
		GraceSeconds:  v.sweepGraceSeconds(),
	})
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

		if err := v.queries.UpsertVeeamSession(ctx, db.UpsertVeeamSessionParams{
			VeeamServerID:   server.ID,
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
		}); err != nil {
			return res, fmt.Errorf("upsert session %s: %w", s.Name, err)
		}
	}

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

	if len(platforms) > 0 {
		v.publishChange(ctx, "sessions_synced")
	}
	return res, nil
}

// sessionWatermark returns the createdAfterFilter for the next poll.
//
// The stored maximum minus an overlap, or — on an empty table — the start of
// the retention window, so a first sync against a server with tens of
// thousands of historical sessions pulls only what Nexara intends to keep.
func (v *VeeamSyncer) sessionWatermark(ctx context.Context, server db.VeeamServer) (time.Time, error) {
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
