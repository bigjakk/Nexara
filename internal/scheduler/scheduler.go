package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/cronspec"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/drs"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/guesttools"
	"github.com/bigjakk/nexara/internal/notifications"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/reports"
	"github.com/bigjakk/nexara/internal/rolling"
	"github.com/bigjakk/nexara/internal/scanner"
	"github.com/bigjakk/nexara/internal/virtiowin"
)

// Scheduler runs due scheduled tasks.
type Scheduler struct {
	queries       *db.Queries
	encryptionKey string
	taskRetention time.Duration
	logger        *slog.Logger
	drsEngine     *drs.Engine
	drsExecutor   *drs.Executor
	cveScanner    *scanner.Engine
	alertEngine   *notifications.Engine
	reportGen     *reports.Generator
	rollingOrch   *rolling.Orchestrator
	virtioWin     *virtiowin.Engine
	guestTools    *guesttools.Engine
	eventPub      *events.Publisher
	cache         *proxmox.ClientCache // nil-safe; passed through to sub-engines
	drsLastEval   map[uuid.UUID]time.Time
}

// Deps are the pre-built domain engines the scheduler ticks. They come from
// the composition root (internal/app) so the HTTP handlers act on the same
// instances — the scheduler no longer constructs any of them itself.
type Deps struct {
	Queries       *db.Queries
	EncryptionKey string
	TaskRetention time.Duration
	Logger        *slog.Logger
	EventPub      *events.Publisher
	Cache         *proxmox.ClientCache

	DRSEngine   *drs.Engine
	DRSExecutor *drs.Executor
	CVEScanner  *scanner.Engine
	AlertEngine *notifications.Engine
	ReportGen   *reports.Generator
	RollingOrch *rolling.Orchestrator
	VirtioWin   *virtiowin.Engine
	GuestTools  *guesttools.Engine
}

// New creates a Scheduler over the shared engines in d.
func New(d Deps) *Scheduler {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		queries:       d.Queries,
		encryptionKey: d.EncryptionKey,
		taskRetention: d.TaskRetention,
		logger:        logger,
		drsEngine:     d.DRSEngine,
		drsExecutor:   d.DRSExecutor,
		cveScanner:    d.CVEScanner,
		alertEngine:   d.AlertEngine,
		reportGen:     d.ReportGen,
		rollingOrch:   d.RollingOrch,
		virtioWin:     d.VirtioWin,
		guestTools:    d.GuestTools,
		eventPub:      d.EventPub,
		cache:         d.Cache,
		drsLastEval:   make(map[uuid.UUID]time.Time),
	}
}

// taskClaimGuardSeconds is how far we push next_run_at into the future
// when claiming a task. This prevents a re-pickup by a subsequent tick
// (or another scheduler instance during a leader-takeover overlap) while
// the run is in flight; the completion path overwrites next_run_at with
// the real cron-computed value via UpdateTaskLastRun.
//
// taskClaimStaleSeconds is how long after the claim we allow the row to
// be reclaimed by another scheduler. Crash recovery: a process that
// takes the claim and dies stops heartbeating last_run_at; once the
// claim ages out, the row becomes eligible again so the task isn't
// stuck in 'running' forever. Set wider than typical task duration but
// narrow enough that a crashed leader's tasks resume on the new leader.
const (
	taskClaimGuardSeconds = 600 // 10 minutes
	taskClaimStaleSeconds = 600 // 10 minutes
)

// dueTaskClaimer is the subset of db.Querier used by claimDueTasks.
// Carved out so the param wiring (guard/stale) and error pass-through
// can be unit-tested with a small fake without touching the rest of
// the scheduler graph.
type dueTaskClaimer interface {
	ClaimDueTasks(ctx context.Context, arg db.ClaimDueTasksParams) ([]db.ScheduledTask, error)
}

// claimDueTasks invokes ClaimDueTasks with the standard guard/stale
// constants. Extracted from Run() so tests can verify the params
// without spinning up a live Postgres.
func claimDueTasks(ctx context.Context, claimer dueTaskClaimer) ([]db.ScheduledTask, error) {
	return claimer.ClaimDueTasks(ctx, db.ClaimDueTasksParams{
		GuardSeconds: taskClaimGuardSeconds,
		StaleSeconds: taskClaimStaleSeconds,
	})
}

// Run finds all due tasks and executes them. Uses an atomic claim
// (SELECT ... FOR UPDATE SKIP LOCKED + status=running + bumped
// next_run_at) so that the same task can't be picked up twice by
// concurrent ticks or by overlapping scheduler instances during a
// leader-takeover window.
func (s *Scheduler) Run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("scheduler Run panicked", "panic", r)
		}
	}()

	tasks, err := claimDueTasks(ctx, s.queries)
	if err != nil {
		s.logger.Error("failed to claim due tasks", "error", err)
		return
	}

	if len(tasks) == 0 {
		return
	}

	s.logger.Info("executing due scheduled tasks", "count", len(tasks))

	// Group tasks by cluster for client reuse.
	byCluster := make(map[uuid.UUID][]db.ScheduledTask)
	for _, t := range tasks {
		byCluster[t.ClusterID] = append(byCluster[t.ClusterID], t)
	}

	for clusterID, clusterTasks := range byCluster {
		client, err := s.createClient(ctx, clusterID)
		if err != nil {
			s.logger.Error("failed to create proxmox client for cluster",
				"cluster_id", clusterID, "error", err)
			for _, t := range clusterTasks {
				s.markFailed(ctx, t, fmt.Sprintf("create client: %v", err))
			}
			continue
		}

		for _, t := range clusterTasks {
			s.executeTask(ctx, client, t)
		}
	}
}

// RunDRS evaluates DRS for all enabled clusters, respecting each cluster's
// configured evaluation interval (eval_interval_seconds).
func (s *Scheduler) RunDRS(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("scheduler RunDRS panicked", "panic", r)
		}
	}()

	configs, err := s.queries.ListEnabledDRSConfigs(ctx)
	if err != nil {
		s.logger.Error("failed to list enabled DRS configs", "error", err)
		return
	}

	if len(configs) == 0 {
		return
	}

	now := time.Now()

	for _, cfg := range configs {
		lastEval, seen := s.drsLastEval[cfg.ClusterID]
		if !drsShouldEvaluate(cfg, lastEval, seen, now) {
			continue
		}

		s.drsLastEval[cfg.ClusterID] = now

		result, err := s.drsEngine.Evaluate(ctx, cfg.ClusterID)
		if err != nil {
			s.logger.Error("DRS evaluation failed",
				"cluster_id", cfg.ClusterID, "error", err)
			// Leave a queued request set so the next tick retries it, unless
			// it has aged past its own interval — otherwise a cluster whose
			// evaluation fails persistently would keep the bypass latched and
			// re-evaluate every tick forever.
			if cfg.EvalRequestedAt.Valid && now.Sub(cfg.EvalRequestedAt.Time) > drsEvalInterval(cfg) {
				s.clearDRSEvalRequest(ctx, cfg)
			}
			continue
		}

		// Evaluation succeeded, so an operator's queued request has been
		// honoured. Clear it against the timestamp we READ rather than now():
		// a request stamped while this pass was running is newer, fails the
		// `<=` guard, and survives to be serviced by the following tick.
		s.clearDRSEvalRequest(ctx, cfg)

		if result != nil && result.BlockedByNativeCRS {
			s.logger.Info("DRS skipped: Proxmox native CRS auto-rebalance is active",
				"cluster_id", cfg.ClusterID)
			continue
		}

		if result == nil || len(result.Recommendations) == 0 {
			continue
		}

		recommendations := result.Recommendations
		s.logger.Info("DRS produced recommendations",
			"cluster_id", cfg.ClusterID, "count", len(recommendations))

		client, err := s.createClient(ctx, cfg.ClusterID)
		if err != nil {
			s.logger.Error("failed to create proxmox client for DRS",
				"cluster_id", cfg.ClusterID, "error", err)
			continue
		}

		if err := s.drsExecutor.Execute(ctx, client, cfg.ClusterID, cfg.Mode, recommendations); err != nil {
			s.logger.Error("DRS execution failed",
				"cluster_id", cfg.ClusterID, "error", err)
		}
	}
}

// defaultDRSEvalInterval applies when a config carries a non-positive
// eval_interval_seconds.
const defaultDRSEvalInterval = 300 * time.Second

// drsEvalInterval is the per-cluster evaluation interval, defaulted.
func drsEvalInterval(cfg db.DrsConfig) time.Duration {
	if interval := time.Duration(cfg.EvalIntervalSeconds) * time.Second; interval > 0 {
		return interval
	}
	return defaultDRSEvalInterval
}

// drsShouldEvaluate decides whether a cluster is due. seen reports whether
// lastEval came from the in-memory map (a cluster never evaluated by this
// process always runs, which is what makes a new leader pick up the whole
// fleet on its first pass).
//
// A queued operator request (eval_requested_at, migration 000079) bypasses the
// interval for exactly one pass — that is the whole reason the manual trigger
// can hand execution to the leader without waiting out the interval.
func drsShouldEvaluate(cfg db.DrsConfig, lastEval time.Time, seen bool, now time.Time) bool {
	if cfg.EvalRequestedAt.Valid {
		return true
	}
	if !seen {
		return true
	}
	return now.Sub(lastEval) >= drsEvalInterval(cfg)
}

// drsEvalRequestClearer is the one-method slice of db.Querier that
// clearDRSEvalRequest needs, carved out so the params can be asserted with a
// fake (same shape as dueTaskClaimer).
type drsEvalRequestClearer interface {
	ClearDRSEvalRequest(ctx context.Context, arg db.ClearDRSEvalRequestParams) error
}

// clearDRSEvalRequestOn clears the queue slot using the timestamp read from
// cfg, never now(): the `eval_requested_at <= $2` guard in the query is what
// keeps a request stamped mid-pass alive for the next tick.
func clearDRSEvalRequestOn(ctx context.Context, clearer drsEvalRequestClearer, cfg db.DrsConfig) error {
	if !cfg.EvalRequestedAt.Valid {
		return nil
	}
	return clearer.ClearDRSEvalRequest(ctx, db.ClearDRSEvalRequestParams{
		ClusterID:       cfg.ClusterID,
		EvalRequestedAt: cfg.EvalRequestedAt,
	})
}

func (s *Scheduler) clearDRSEvalRequest(ctx context.Context, cfg db.DrsConfig) {
	if err := clearDRSEvalRequestOn(ctx, s.queries, cfg); err != nil {
		s.logger.Warn("failed to clear DRS evaluation request",
			"cluster_id", cfg.ClusterID, "error", err)
	}
}

// RunKEVRefresh refreshes the local CISA KEV catalog cache. The feed is small
// (~1500 entries) and updated multiple times per week — refreshing hourly is
// cheap and keeps the "actively exploited" signal current. Best-effort:
// failures are logged but don't impact scans (existing cache stays usable).
//
// Reuses the scanner Engine's shared KEV client (and its underlying HTTP
// client) so the hourly refresh and the per-cluster scan share a connection
// pool, redirect policy, and SSRF dial guard.
func (s *Scheduler) RunKEVRefresh(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("KEV refresh panicked", "panic", r)
		}
	}()

	kev := s.cveScanner.KEVClient()
	written, err := kev.Refresh(ctx)
	if err != nil {
		s.logger.Warn("KEV refresh failed", "error", err)
		return
	}
	s.logger.Info("KEV refresh complete", "entries", written)
}

// virtioWinHistoryRetention bounds how long finished virtio-win download rows
// are kept. Downloads are infrequent (one per release per cluster), so this is
// generous enough to stay a useful audit trail.
const virtioWinHistoryRetention = 90 * 24 * time.Hour

// RunVirtioWinCheck refreshes the upstream virtio-win catalog and brings every
// cluster whose check has come due in line with its target version.
//
// One upstream fetch serves every cluster: the catalog is global, and only the
// per-cluster reconciliation against storage is fanned out. Downloads are
// dispatched, not awaited — download-url returns a UPID and RunVirtioWinReconcile
// follows it, so an 837 MiB transfer never holds this tick open.
//
// The tick itself is now a cheap minutely poll rather than a six-hourly sweep:
// each cluster carries its own next_check_at, so an operator can aim the check
// — and the ~840 MiB fetch it dispatches — at a maintenance window. When
// nothing is due this costs one indexed query and returns.
func (s *Scheduler) RunVirtioWinCheck(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("virtio-win check panicked", "panic", r)
		}
	}()
	// Deferred, so retention covers the early returns below too: at the tail
	// it ran only on a tick that found work, and the tick that finds nothing
	// due is exactly the one a switched-off cluster produces forever.
	defer s.virtioWin.TrimHistory(ctx, virtioWinHistoryRetention)

	// Check who wants this BEFORE touching the network. Refreshing
	// unconditionally would make every install — air-gapped ones included —
	// reach out to fedorapeople.org for a feature nobody enabled. No cluster
	// due, no outbound request.
	configs, err := s.queries.ListDueVirtioWinConfigs(ctx)
	if err != nil {
		s.logger.Warn("virtio-win: list due configs failed", "error", err)
		return
	}
	if len(configs) == 0 {
		return
	}

	latest, err := s.virtioWin.RefreshCatalog(ctx)
	if err != nil {
		// Keep going: a cluster may still be behind on a version already in the
		// catalog, and that is worth acting on even when upstream is unreachable.
		s.logger.Warn("virtio-win: upstream catalog refresh failed", "error", err)
	} else {
		s.logger.Debug("virtio-win: catalog refreshed", "latest", latest.Version, "stable", latest.IsStable)
	}
	for _, cfg := range configs {
		download, syncErr := s.virtioWin.SyncCluster(ctx, cfg)
		// Unconditional, and before the logging: this is what arms the next
		// check. Skipping it on any path leaves next_check_at in the past, and
		// the cluster is then re-synced every single minute.
		s.virtioWin.MarkChecked(ctx, cfg, syncErr)
		switch {
		case syncErr != nil:
			s.logger.Warn("virtio-win: cluster sync failed",
				"cluster_id", cfg.ClusterID, "storage", cfg.Storage, "error", syncErr)
		case download != nil:
			s.logger.Info("virtio-win: download dispatched",
				"cluster_id", cfg.ClusterID, "storage", cfg.Storage,
				"version", download.Version, "node", download.Node, "upid", download.Upid)
			s.trackTask(ctx, virtioWinDownloadTask(*download))
		}
	}
}

// RunGuestToolsPass detects installed guest tools across every cluster with the
// feature on, and stages updates for guests that are behind.
//
// Hourly rather than minutely: this reaches into every running Windows guest
// with a registry read, and nothing about a driver version changes on a shorter
// timescale than an operator installing something.
func (s *Scheduler) RunGuestToolsPass(ctx context.Context) {
	s.tick(ctx, "guest tools pass panicked", "guest tools: pass failed", s.guestTools.RunPass)
}

// RunGuestToolsReconcile advances guests with an update in flight.
//
// Separate from the pass, and far more frequent, because a staged install fires
// on the guest's own reboot — which Nexara neither triggers nor observes. The
// only way to learn the outcome is to keep checking for the result file the
// in-guest updater leaves behind.
func (s *Scheduler) RunGuestToolsReconcile(ctx context.Context) {
	s.tick(ctx, "guest tools reconcile panicked", "guest tools: reconcile failed", s.guestTools.Reconcile)
}

// RunVirtioWinReconcile advances in-flight virtio-win downloads by polling the
// Proxmox tasks they became. Separate from the check tick because the check is
// a 6-hourly upstream poll while a download needs following minute by minute —
// and because the transfer outlives any in-process watcher.
func (s *Scheduler) RunVirtioWinReconcile(ctx context.Context) {
	s.tick(ctx, "virtio-win reconcile panicked", "virtio-win: reconcile failed", s.virtioWin.Reconcile)
}

// tick runs one engine pass, isolating the scheduler from it: a panic is
// logged instead of taking the process down, and a returned error is a warning
// rather than a reason to stop ticking.
//
// No nil check on the engine: internal/app builds every one of them whenever
// Queries is set, and the binary exits before that if it cannot reach the
// database — so a nil here is unreachable, and pretending otherwise made the
// guards read as a defence the unguarded call sites did not have.
func (s *Scheduler) tick(ctx context.Context, panicMsg, failMsg string, run func(context.Context) error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error(panicMsg, "panic", r)
		}
	}()
	if err := run(ctx); err != nil {
		s.logger.Warn(failMsg, "error", err)
	}
}

// virtioWinDownloadTask describes a dispatched virtio-win download.
func virtioWinDownloadTask(row db.VirtioWinDownload) trackTaskParams {
	return trackTaskParams{
		ClusterID:    row.ClusterID,
		Node:         row.Node,
		ResourceType: "storage",
		ResourceID:   row.Storage,
		Action:       "virtio_win_download",
		UPID:         row.Upid,
		TaskType:     "download",
		Description:  "Download virtio-win " + row.Version + " to " + row.Storage,
		Source:       slog.String("download_id", row.ID.String()),
		Details: map[string]string{
			"upid":     row.Upid,
			"node":     row.Node,
			"storage":  row.Storage,
			"version":  row.Version,
			"filename": row.Filename,
		},
	}
}

// RunReportRetention deletes scheduled-report runs older than 90 days. The
// retention SQL was defined alongside the report generator but never invoked
// — without this tick, scheduled-report rows accumulate forever. On-demand
// runs (schedule_id IS NULL) are preserved unconditionally.
func (s *Scheduler) RunReportRetention(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("report retention panicked", "panic", r)
		}
	}()
	if err := s.queries.CleanupOldReportRuns(ctx); err != nil {
		s.logger.Warn("report retention failed", "error", err)
		return
	}
	s.logger.Debug("report retention complete")
}

// RunTaskRetention deletes terminal task_history rows older than the configured
// TASK_HISTORY_RETENTION window. Mirrors RunReportRetention; the underlying
// delete is guarded on status != 'running', so in-flight tasks (long disk
// moves, migrations) are never removed. Without this tick, completed/failed
// task rows would accumulate forever — ClearCompleted is manual-only.
func (s *Scheduler) RunTaskRetention(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("task retention panicked", "panic", r)
		}
	}()
	if err := s.queries.DeleteCompletedTasks(ctx, time.Now().Add(-s.taskRetention)); err != nil {
		s.logger.Warn("task retention failed", "error", err)
		return
	}
	s.logger.Debug("task retention complete")
}

// RunCVEScanning runs CVE scans for clusters based on their schedule configuration.
// Clusters with no schedule config default to enabled with a 24-hour interval.
func (s *Scheduler) RunCVEScanning(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("scheduler RunCVEScanning panicked", "panic", r)
		}
	}()

	// Self-heal scans stuck in running/pending by a crash or restart —
	// they block manual triggers and post-rolling-update rescans.
	if swept, err := s.queries.FailStaleCVEScans(ctx); err != nil {
		s.logger.Warn("failed to sweep stale CVE scans", "error", err)
	} else if swept > 0 {
		s.logger.Info("swept stale CVE scans", "count", swept)
	}

	clusters, err := s.queries.ListClusters(ctx)
	if err != nil {
		s.logger.Error("failed to list clusters for CVE scanning", "error", err)
		return
	}

	// Build a map of schedule configs for quick lookup
	schedules := make(map[uuid.UUID]struct {
		enabled  bool
		interval time.Duration
	})
	scheds, err := s.queries.ListEnabledCVEScanSchedules(ctx)
	if err != nil {
		s.logger.Warn("failed to list CVE scan schedules, using defaults", "error", err)
	}
	for _, sc := range scheds {
		schedules[sc.ClusterID] = struct {
			enabled  bool
			interval time.Duration
		}{enabled: sc.Enabled, interval: time.Duration(sc.IntervalHours) * time.Hour}
	}

	for _, cluster := range clusters {
		if !cluster.IsActive {
			continue
		}

		// Look up schedule config; default to enabled / 24h
		sched, hasConfig := schedules[cluster.ID]
		if hasConfig {
			if !sched.enabled {
				continue
			}
		} else {
			// Check if there's a disabled schedule (not in the "enabled" list)
			cfg, cfgErr := s.queries.GetCVEScanSchedule(ctx, cluster.ID)
			if cfgErr == nil && !cfg.Enabled {
				continue
			}
			sched.interval = 24 * time.Hour
		}

		// Check if last scan was within the configured interval
		lastScan, err := s.queries.GetLatestCVEScan(ctx, cluster.ID)
		if err == nil && time.Since(lastScan.CreatedAt) < sched.interval {
			continue
		}

		s.logger.Info("starting CVE scan", "cluster_id", cluster.ID, "cluster_name", cluster.Name)

		scanID, err := s.cveScanner.ScanCluster(ctx, cluster.ID)
		if err != nil {
			s.logger.Error("CVE scan failed",
				"cluster_id", cluster.ID, "error", err)
			continue
		}

		s.logger.Info("CVE scan completed",
			"cluster_id", cluster.ID, "scan_id", scanID)

		if s.eventPub != nil {
			s.eventPub.ClusterEvent(ctx, cluster.ID.String(), events.KindCVEScan, "cve_scan", scanID.String(), "completed")
		}
	}
}

// snapshotParams holds decoded params for snapshot actions.
type snapshotParams struct {
	SnapName    string `json:"snap_name"`
	Description string `json:"description"`
	VMState     bool   `json:"vmstate"`
}

func (s *Scheduler) executeTask(ctx context.Context, client *proxmox.Client, task db.ScheduledTask) {
	now := time.Now()
	var execErr error

	switch task.Action {
	case "snapshot":
		execErr = s.executeSnapshot(ctx, client, task)
	case "reboot":
		execErr = s.executeReboot(ctx, client, task)
	default:
		execErr = fmt.Errorf("unsupported action: %s", task.Action)
	}

	status := "success"
	errMsg := ""
	if execErr != nil {
		status = "failed"
		errMsg = execErr.Error()
		s.logger.Error("scheduled task failed",
			"task_id", task.ID, "action", task.Action, "error", execErr)
	} else {
		s.logger.Info("scheduled task completed",
			"task_id", task.ID, "action", task.Action)
	}

	s.finishTaskRun(ctx, task, now, status, errMsg)
}

// finishTaskRun records how a run ended and arms the next one.
//
// now is when the run STARTED, not when it finished, so a long run does not
// push its own next occurrence out by its duration.
//
// When the cron can no longer yield a future run there is no safe value to
// write: a NULL next_run_at reads as "due now" in this table's due predicate,
// which would claim and RE-RUN this task — the reboot or snapshot it carries —
// on every tick. Park it instead, with the reason on the row. status/errMsg
// ride along: the run that just finished has its own outcome, independent of
// the schedule being unusable. A snapshot that succeeded must not be recorded
// as failed just because the task is being parked, and a run that DID fail
// must not have its reason replaced by the schedule message — last_error is
// the only field the operator sees.
func (s *Scheduler) finishTaskRun(ctx context.Context, task db.ScheduledTask, now time.Time, status, errMsg string) {
	nextRun, cronErr := cronspec.NextRunTime(task.Schedule, now)
	if cronErr != nil {
		s.parkUnschedulableTask(ctx, task, cronErr, status, errMsg)
		return
	}

	if err := s.queries.UpdateTaskLastRun(ctx, db.UpdateTaskLastRunParams{
		ID:         task.ID,
		LastRunAt:  pgtype.Timestamptz{Time: now, Valid: true},
		NextRunAt:  pgtype.Timestamptz{Time: nextRun, Valid: true},
		LastStatus: pgtype.Text{String: status, Valid: true},
		LastError:  pgtype.Text{String: errMsg, Valid: errMsg != ""},
	}); err != nil {
		s.logger.Error("failed to update task last run", "task_id", task.ID, "error", err)
	}
}

func (s *Scheduler) executeSnapshot(ctx context.Context, client *proxmox.Client, task db.ScheduledTask) error {
	var params snapshotParams
	if err := json.Unmarshal(task.Params, &params); err != nil {
		return fmt.Errorf("unmarshal snapshot params: %w", err)
	}

	snapName := params.SnapName
	if snapName == "" {
		snapName = fmt.Sprintf("auto-%s", time.Now().Format("20060102-150405"))
	}

	sp := proxmox.SnapshotParams{
		SnapName:    snapName,
		Description: params.Description,
		VMState:     params.VMState,
	}

	var upid string
	var err error
	switch task.ResourceType {
	case "vm":
		upid, err = client.CreateVMSnapshot(ctx, task.Node, mustAtoi(task.ResourceID), sp)
	case "ct":
		upid, err = client.CreateCTSnapshot(ctx, task.Node, mustAtoi(task.ResourceID), sp)
	default:
		return fmt.Errorf("unsupported resource type for snapshot: %s", task.ResourceType)
	}
	if err != nil {
		return err
	}
	s.trackTask(ctx, scheduledTask(task, upid, "scheduled_snapshot",
		fmt.Sprintf("Scheduled snapshot %s — %s %s", snapName, task.ResourceType, task.ResourceID)))
	return nil
}

func (s *Scheduler) executeReboot(ctx context.Context, client *proxmox.Client, task db.ScheduledTask) error {
	vmid := mustAtoi(task.ResourceID)
	var upid string
	var err error
	switch task.ResourceType {
	case "vm":
		upid, err = client.RebootVM(ctx, task.Node, vmid)
	case "ct":
		upid, err = client.RebootCT(ctx, task.Node, vmid)
	default:
		return fmt.Errorf("unsupported resource type for reboot: %s", task.ResourceType)
	}
	if err != nil {
		return err
	}
	s.trackTask(ctx, scheduledTask(task, upid, "scheduled_reboot",
		fmt.Sprintf("Scheduled reboot — %s %s", task.ResourceType, task.ResourceID)))
	return nil
}

// trackTaskParams is one UPID-producing action the scheduler dispatched on its
// own behalf. Built by scheduledTask and virtioWinDownloadTask so the recording
// below happens once, whatever produced the UPID.
type trackTaskParams struct {
	ClusterID    uuid.UUID
	Node         string
	ResourceType string
	ResourceID   string
	Action       string
	UPID         string
	TaskType     string
	Description  string
	Details      map[string]string
	// Source identifies the row that dispatched this in the warnings below —
	// task_id for a schedule, download_id for a download — so each keeps the
	// key every other log line about that row already uses.
	Source slog.Attr
}

// scheduledTask describes a UPID a scheduled_tasks row produced.
func scheduledTask(task db.ScheduledTask, upid, taskType, description string) trackTaskParams {
	return trackTaskParams{
		ClusterID:    task.ClusterID,
		Node:         task.Node,
		ResourceType: task.ResourceType,
		ResourceID:   task.ResourceID,
		Action:       taskType,
		UPID:         upid,
		TaskType:     taskType,
		Description:  description,
		Source:       slog.String("task_id", task.ID.String()),
		Details: map[string]string{
			"upid":        upid,
			"schedule_id": task.ID.String(),
			"node":        task.Node,
		},
	}
}

// trackTask records a UPID the scheduler produced the same way the DRS executor
// records its migrations: a task_history row (status running — the collector
// reconciler owns the running→done flip) and an audit entry attributed to the
// system user, each followed by the event that makes it visible live —
// task_created and audit_entry. Previously the UPID was discarded, so the
// action was invisible until the collector re-ingested it as an external
// "proxmox" task mis-attributed to the API token user.
//
// If the task_history insert fails, the event and audit row are skipped on
// purpose: an audit row containing the UPID would make the external-task
// ingest dedup skip it, and the task would then never appear anywhere. With
// nothing recorded, the next collector tick ingests it as external — a
// degraded but visible fallback.
func (s *Scheduler) trackTask(ctx context.Context, p trackTaskParams) {
	if p.UPID == "" {
		return
	}
	if _, err := s.queries.InsertTaskHistory(ctx, db.InsertTaskHistoryParams{
		ClusterID:   p.ClusterID,
		UserID:      auth.SystemUserID,
		Upid:        p.UPID,
		Description: p.Description,
		Status:      "running",
		Node:        p.Node,
		TaskType:    p.TaskType,
	}); err != nil {
		s.logger.Warn("failed to insert task history for a scheduler-dispatched task",
			p.Source, "action", p.Action, "upid", p.UPID, "error", err)
		return
	}

	details, _ := json.Marshal(p.Details)
	if err := s.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: p.ClusterID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: p.ResourceType,
		ResourceID:   p.ResourceID,
		Action:       p.Action,
		Details:      details,
	}); err != nil {
		s.logger.Warn("failed to insert audit log for a scheduler-dispatched task",
			p.Source, "action", p.Action, "upid", p.UPID, "error", err)
	}

	if s.eventPub != nil {
		s.eventPub.ClusterEvent(ctx, p.ClusterID.String(),
			events.KindTaskCreated, "task", p.UPID, p.Action)
		s.eventPub.ClusterEvent(ctx, p.ClusterID.String(),
			events.KindAuditEntry, p.ResourceType, p.ResourceID, p.Action)
	}
}

// createClient returns a cached *Client when the composition root supplied a
// cache (Deps.Cache), falling back to a per-call build. The sub-engines get
// the same cache wired directly in app.New, so nothing propagates it here.
func (s *Scheduler) createClient(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, error) {
	if s.cache != nil {
		client, err := s.cache.Get(ctx, clusterID)
		if err == nil {
			return client, nil
		}
		s.logger.Warn("scheduler: proxmox cache get failed, building per-call",
			"cluster_id", clusterID, "error", err)
	}

	return proxmox.NewClientForCluster(ctx, s.queries, s.encryptionKey, clusterID, 60*time.Second)
}

// markFailed records a run that never reached an engine — the Proxmox client
// could not be built, so nothing was dispatched. The run still finished, and
// finishTaskRun still has to arm the next one.
func (s *Scheduler) markFailed(ctx context.Context, task db.ScheduledTask, errMsg string) {
	s.finishTaskRun(ctx, task, time.Now(), "failed", errMsg)
}

// parkUnschedulableTask disables a task whose cron cannot yield a future run,
// recording why on the row.
//
// Disabling is the only value that breaks the loop here. scheduled_tasks
// matches `next_run_at IS NULL OR next_run_at <= now()`, so neither NULL nor
// the zero time robfig returns for an unsatisfiable expression is inert — both
// mean due now. The API rejects such an expression on write, so reaching this
// is a row from before that check existed, or one edited by hand.
// runStatus and runErr describe the run that just finished; they are recorded
// as-is rather than being replaced by the schedule problem, because the two are
// independent. A snapshot can be taken perfectly by a task whose expression can
// never come round again — writing 'failed' there sends the operator looking
// for a problem in the wrong half — and a run that genuinely failed must keep
// its own reason, since last_error is the only field that surfaces either.
func (s *Scheduler) parkUnschedulableTask(
	ctx context.Context,
	task db.ScheduledTask,
	cronErr error,
	runStatus string,
	runErr string,
) {
	s.logger.Error("scheduled task disabled: its schedule can never fire",
		"task_id", task.ID, "action", task.Action, "schedule", task.Schedule,
		"error", cronErr, "run_status", runStatus, "run_error", runErr)
	msg := "disabled: " + cronErr.Error()
	if runErr != "" {
		msg = runErr + "; " + msg
	}
	if err := s.queries.DisableScheduledTaskForBadSchedule(ctx, db.DisableScheduledTaskForBadScheduleParams{
		ID:         task.ID,
		LastStatus: pgtype.Text{String: runStatus, Valid: runStatus != ""},
		LastError:  pgtype.Text{String: msg, Valid: true},
	}); err != nil {
		s.logger.Error("failed to disable task with an unusable schedule",
			"task_id", task.ID, "error", err)
	}
}

// RunAlertEvaluation evaluates all enabled alert rules against current metrics.
func (s *Scheduler) RunAlertEvaluation(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("alert evaluation panicked", "panic", r)
		}
	}()
	s.alertEngine.Evaluate(ctx)
}

// RunReportGeneration checks for due report schedules and generates reports.
func (s *Scheduler) RunReportGeneration(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("report generation panicked", "panic", r)
		}
	}()

	schedules, err := s.queries.ListDueReportSchedules(ctx)
	if err != nil {
		s.logger.Error("failed to list due report schedules", "error", err)
		return
	}

	if len(schedules) == 0 {
		return
	}

	s.logger.Info("processing due report schedules", "count", len(schedules))

	for _, sched := range schedules {
		s.generateScheduledReport(ctx, sched)
	}
}

func (s *Scheduler) generateScheduledReport(ctx context.Context, sched db.ReportSchedule) {
	params, pErr := reports.ParseParams(sched.Parameters)
	if pErr != nil {
		// The API validates parameters on write, so this is a row edited by
		// hand. Run with the defaults and say so rather than skip the report.
		s.logger.Warn("report schedule has invalid parameters, using defaults", "schedule_id", sched.ID, "error", pErr)
		params = reports.DefaultParams()
	}
	paramsJSON := sched.Parameters
	if len(paramsJSON) == 0 {
		paramsJSON = json.RawMessage(`{}`)
	}

	run, err := s.queries.InsertReportRun(ctx, db.InsertReportRunParams{
		ScheduleID:     pgtype.UUID{Bytes: sched.ID, Valid: true},
		ReportType:     sched.ReportType,
		ClusterID:      sched.ClusterID,
		Status:         "running",
		TimeRangeHours: sched.TimeRangeHours,
		Parameters:     paramsJSON,
		CreatedBy:      sched.CreatedBy,
	})
	if err != nil {
		s.logger.Error("failed to create report run", "schedule_id", sched.ID, "error", err)
		return
	}

	_ = s.queries.UpdateReportRunStarted(ctx, run.ID)

	// The run reads under the grants of whoever last saved the schedule
	// (run_as), and only while that account is active: a deactivated user's
	// grants must not keep producing reports on their behalf.
	runAs, uErr := s.queries.GetUserByID(ctx, sched.RunAs)
	if uErr != nil || !runAs.IsActive {
		_ = s.queries.UpdateReportRunFailed(ctx, db.UpdateReportRunFailedParams{
			ID:           run.ID,
			ErrorMessage: "the user this schedule runs as is deactivated or gone; save the schedule again as an active user",
		})
		s.logger.Warn("report schedule skipped: run_as user inactive or missing", "schedule_id", sched.ID, "run_as", sched.RunAs, "error", uErr)
		s.updateScheduleNextRun(ctx, sched)
		return
	}
	data, err := s.reportGen.Generate(ctx, reports.Request{
		Type:           sched.ReportType,
		ClusterID:      sched.ClusterID,
		TimeRangeHours: int(sched.TimeRangeHours),
		Params:         params,
		RequestedBy:    sched.RunAs,
	})
	if err != nil {
		_ = s.queries.UpdateReportRunFailed(ctx, db.UpdateReportRunFailedParams{
			ID:           run.ID,
			ErrorMessage: err.Error(),
		})
		s.logger.Error("report generation failed", "schedule_id", sched.ID, "error", err)
		s.updateScheduleNextRun(ctx, sched)
		return
	}

	htmlOutput, err := reports.RenderHTML(data)
	if err != nil {
		_ = s.queries.UpdateReportRunFailed(ctx, db.UpdateReportRunFailedParams{
			ID:           run.ID,
			ErrorMessage: fmt.Sprintf("render HTML: %v", err),
		})
		s.updateScheduleNextRun(ctx, sched)
		return
	}

	csvOutput, err := reports.RenderCSV(data)
	if err != nil {
		_ = s.queries.UpdateReportRunFailed(ctx, db.UpdateReportRunFailedParams{
			ID:           run.ID,
			ErrorMessage: fmt.Sprintf("render CSV: %v", err),
		})
		s.updateScheduleNextRun(ctx, sched)
		return
	}

	dataJSON, _ := json.Marshal(data)

	if err := s.queries.UpdateReportRunCompleted(ctx, db.UpdateReportRunCompletedParams{
		ID:         run.ID,
		ReportData: dataJSON,
		ReportHtml: pgtype.Text{String: htmlOutput, Valid: true},
		ReportCsv:  pgtype.Text{String: csvOutput, Valid: true},
	}); err != nil {
		s.logger.Error("failed to save report", "run_id", run.ID, "error", err)
	}

	s.logger.Info("scheduled report generated",
		"schedule_id", sched.ID, "run_id", run.ID, "type", sched.ReportType)

	// Email: the digest as the body, the report attached. The schedule's
	// format decides the attachments — HTML always, CSV alongside it when the
	// schedule asks for CSV.
	if sched.EmailEnabled && sched.EmailChannelID.Valid {
		channelID, _ := uuid.FromBytes(sched.EmailChannelID.Bytes[:])
		msg := reports.ReportMessage(data, htmlOutput, csvOutput, sched.Format == "csv", reports.DigestOptions{
			ScheduleName: sched.Name,
			RunID:        run.ID.String(),
		})
		if err := reports.SendReportEmail(ctx, s.queries, s.encryptionKey, channelID, sched.EmailRecipients, msg, s.logger); err != nil {
			s.logger.Error("failed to send report email", "schedule_id", sched.ID, "error", err)
		}
	}

	if s.eventPub != nil {
		s.eventPub.SystemEvent(ctx, events.KindReportGenerated, "completed")
	}

	s.updateScheduleNextRun(ctx, sched)
}

func (s *Scheduler) updateScheduleNextRun(ctx context.Context, sched db.ReportSchedule) {
	now := time.Now()
	nextRun, err := cronspec.NextRunTime(sched.Schedule, now)
	if err != nil {
		// Unlike scheduled_tasks, this table's due predicate is a bare
		// `next_run_at <= now()`, so NULL is genuinely inert and leaving the
		// row enabled with no next run is a safe stop rather than a loop. Say
		// so loudly all the same: from the UI it just stops generating.
		s.logger.Error("report schedule will not run again: its schedule can never fire",
			"schedule_id", sched.ID, "schedule", sched.Schedule, "error", err)
	}
	next := pgtype.Timestamptz{Time: nextRun, Valid: err == nil}

	if uErr := s.queries.UpdateReportScheduleLastRun(ctx, db.UpdateReportScheduleLastRunParams{
		ID:        sched.ID,
		LastRunAt: pgtype.Timestamptz{Time: now, Valid: true},
		NextRunAt: next,
	}); uErr != nil {
		s.logger.Error("failed to update schedule last run", "schedule_id", sched.ID, "error", uErr)
	}
}

// RunRollingUpdates advances any running rolling update jobs.
func (s *Scheduler) RunRollingUpdates(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("rolling update orchestrator panicked", "panic", r)
		}
	}()
	s.rollingOrch.Tick(ctx)
}

// RunVMImportReconcile polls the Proxmox task behind each active VM-import job and flips
// the job to completed/failed once the create-with-import task reaches a terminal state.
// This is what gives the import history a durable status across process restarts (the
// dispatching API request returns as soon as the long disk conversion has started).
func (s *Scheduler) RunVMImportReconcile(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("vm import reconcile panicked", "panic", r)
		}
	}()

	// Fail any jobs whose dispatch was interrupted before a UPID was recorded — they have
	// no task to reconcile against and would otherwise sit 'pending' forever.
	if err := s.queries.FailStalePendingVMImportJobs(ctx); err != nil {
		s.logger.Warn("vm import reconcile: stale-pending sweep failed", "error", err)
	}

	jobs, err := s.queries.ListActiveVMImportJobs(ctx)
	if err != nil {
		s.logger.Error("vm import reconcile: list active jobs failed", "error", err)
		return
	}
	if len(jobs) == 0 {
		return
	}

	clients := make(map[uuid.UUID]*proxmox.Client)
	for _, job := range jobs {
		client, seen := clients[job.ClusterID]
		if !seen {
			c, cerr := s.createClient(ctx, job.ClusterID)
			if cerr != nil {
				s.logger.Warn("vm import reconcile: create client failed", "cluster_id", job.ClusterID, "error", cerr)
				clients[job.ClusterID] = nil
				continue
			}
			client = c
			clients[job.ClusterID] = c
		}
		if client == nil {
			continue
		}
		s.reconcileImportJob(ctx, client, job)
	}
}

func (s *Scheduler) reconcileImportJob(ctx context.Context, client *proxmox.Client, job db.VmImportJob) {
	status, err := client.GetTaskStatus(ctx, job.TargetNode, job.Upid)
	if err != nil {
		s.logger.Warn("vm import reconcile: task status failed", "job_id", job.ID, "upid", job.Upid, "error", err)
		return
	}
	if status.Status != "stopped" {
		return // still running
	}
	// proxmox.TaskSucceeded is the single source of truth for the success rule —
	// it treats empty and "WARNINGS: N" exit statuses as success, which imports
	// (EFI-state-lost, guest-was-running, etc.) routinely emit.
	if proxmox.TaskSucceeded(status.ExitStatus) {
		if err := s.queries.CompleteVMImportJob(ctx, job.ID); err != nil {
			s.logger.Error("vm import reconcile: complete failed", "job_id", job.ID, "error", err)
			return
		}
		s.publishImport(ctx, job, "completed")
		return
	}
	reason := status.ExitStatus
	if reason == "" {
		reason = "import task failed"
	}
	if err := s.queries.FailVMImportJob(ctx, db.FailVMImportJobParams{ID: job.ID, FailureReason: reason}); err != nil {
		s.logger.Error("vm import reconcile: fail update failed", "job_id", job.ID, "error", err)
		return
	}
	s.publishImport(ctx, job, "failed")
}

func (s *Scheduler) publishImport(ctx context.Context, job db.VmImportJob, action string) {
	if s.eventPub == nil {
		return
	}
	s.eventPub.ClusterEvent(ctx, job.ClusterID.String(), events.KindVMImport, "vm_import", job.ID.String(), action)
	if action == "completed" {
		// A newly-imported VM should surface in the inventory promptly.
		s.eventPub.ClusterEvent(ctx, job.ClusterID.String(), events.KindInventoryChange, "vm", "", "import_completed")
	}
}

// mustAtoi converts a string to int, returning 0 on failure.
func mustAtoi(s string) int {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
