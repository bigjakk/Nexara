package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// cleanupCtxFor returns (ctx, no-op cancel) when ctx is still alive, or a
// fresh 5-second timeout context derived from Background when the parent
// is already cancelled. Use it for the DB / event / audit-log writes that
// record a failure outcome — without it, a SIGTERM cancellation would
// leave a migration row in 'migrating' status forever because the DB
// write silently no-ops on a cancelled context.
func cleanupCtxFor(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// defaultPollInterval and defaultPollStaleGrace are pollTaskStatus's timings.
//
// The grace deliberately equals collector.staleTaskGrace. That sweep used to
// be what finalized a migration's task row when Proxmox stopped being able to
// report on it — "node reboot, task-log rotation", in its own words — and
// withholding cross-cluster rows from the collector took it away. This
// restores an equivalent inside the only component still watching the row.
// They are separate constants because neither package depends on the other;
// change one and change the other.
//
// Applied to CONSECUTIVE poll failures rather than to total runtime. That is
// more CONSERVATIVE than the sweep it replaces, not stricter — it abandons
// strictly fewer rows, because the old sweep fired on a single error once the
// task was 24h old, while this needs 24h of unbroken failure. The reason is
// that a cross-cluster migration can legitimately
// run for many hours, and a deadline on the whole loop would mark a healthy
// long transfer as failed while it was still copying. A successful poll clears
// the timer, so only a migration Proxmox has genuinely stopped answering for
// hits it.
const (
	defaultPollInterval   = 5 * time.Second
	defaultPollStaleGrace = 24 * time.Hour
)

// Orchestrator manages migration job execution.
type Orchestrator struct {
	queries       *db.Queries
	encryptionKey string
	cache         *proxmox.ClientCache // nil-safe; falls back to per-call construction
	logger        *slog.Logger
	eventPub      *events.Publisher

	// pollTaskStatus's timings. Fields rather than constants only so a test
	// can drive the loop without waiting out a 5s tick or a 24h grace;
	// production never sets them and NewOrchestrator supplies the defaults.
	pollInterval   time.Duration
	pollStaleGrace time.Duration
}

// NewOrchestrator creates a new migration orchestrator.
func NewOrchestrator(queries *db.Queries, encryptionKey string, logger *slog.Logger, eventPub *events.Publisher) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{
		queries:        queries,
		encryptionKey:  encryptionKey,
		logger:         logger,
		eventPub:       eventPub,
		pollInterval:   defaultPollInterval,
		pollStaleGrace: defaultPollStaleGrace,
	}
}

// SetProxmoxCache attaches the shared per-server cache. Nil-safe.
func (o *Orchestrator) SetProxmoxCache(cache *proxmox.ClientCache) {
	o.cache = cache
}

// migrationContext holds resolved metadata used for task tracking and audit logging.
type migrationContext struct {
	job     db.MigrationJob
	vmDBID  string // VM's database UUID (for audit log resource linking)
	vmName  string // Human-readable VM name
	vmLabel string // e.g. "VM 100 (my-vm)" or "CT 200 (my-ct)"
	userID  uuid.UUID

	// scrubber removes any credential this job put in play from text on its
	// way to a persisted field. It is nil for a job that never assembled one
	// (everything but cross-cluster), so reach it through scrub, not
	// directly. A closure rather than the secret itself: nothing that prints
	// a migrationContext can then render the credential.
	scrubber func(string) string
}

// scrub cleans text that is about to be persisted or logged. Nil-safe in both
// directions — a nil context and an unarmed one both mean "no credential in
// play", which is the truth for every non-cross-cluster job.
func (mc *migrationContext) scrub(s string) string {
	if mc == nil || mc.scrubber == nil {
		return s
	}
	return mc.scrubber(s)
}

// armEndpointScrubber points mc's scrubber at the target cluster's token
// secret. Called once, where the credential is assembled, so that everything
// this job later writes about a Proxmox failure is covered by construction
// rather than by each sink remembering.
func (mc *migrationContext) armEndpointScrubber(apiToken string) {
	if mc == nil {
		return
	}
	secret := endpointTokenSecret(apiToken)
	// Not redundant with scrubSecret's guard: arming with an empty secret
	// would install a closure that does nothing, and mc.scrubber != nil would
	// then lie about whether this job has a credential in play.
	if secret == "" {
		return
	}
	mc.scrubber = func(s string) string { return scrubSecret(s, secret) }
}

// clientForCluster creates a Proxmox client from stored cluster credentials.
// Prefers the shared cache when available; falls back to per-call construction
// otherwise. The cluster row is also returned so callers can read auxiliary
// fields (Name, ApiUrl) without an extra DB roundtrip on the cache-hit path.
func (o *Orchestrator) clientForCluster(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, db.Cluster, error) {
	cluster, err := o.queries.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, cluster, fmt.Errorf("get cluster %s: %w", clusterID, err)
	}

	if o.cache != nil {
		if client, err := o.cache.Get(ctx, clusterID); err == nil {
			return client, cluster, nil
		} else {
			o.logger.Warn("migration: proxmox cache get failed, building per-call",
				"cluster_id", clusterID, "error", err)
		}
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, o.encryptionKey)
	if err != nil {
		return nil, cluster, fmt.Errorf("decrypt cluster credentials: %w", err)
	}

	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        5 * time.Minute,
	})
	if err != nil {
		return nil, cluster, fmt.Errorf("create proxmox client for cluster %s: %w", cluster.Name, err)
	}

	return client, cluster, nil
}

// resolveMigrationContext looks up the VM in the DB to get its name and UUID for audit/task logging.
func (o *Orchestrator) resolveMigrationContext(ctx context.Context, job db.MigrationJob, userID uuid.UUID) migrationContext {
	mc := migrationContext{
		job:    job,
		userID: userID,
	}

	typeLabel := "VM"
	if job.VmType == VMTypeLXC {
		typeLabel = "CT"
	}

	// Try to look up the VM in the DB for its name and UUID.
	vm, err := o.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
		ClusterID: job.SourceClusterID,
		Vmid:      job.Vmid,
	})
	if err == nil {
		mc.vmDBID = vm.ID.String()
		mc.vmName = vm.Name
		mc.vmLabel = fmt.Sprintf("%s %d (%s)", typeLabel, job.Vmid, vm.Name)
	} else {
		mc.vmDBID = ""
		mc.vmLabel = fmt.Sprintf("%s %d", typeLabel, job.Vmid)
	}

	return mc
}

// RunPreFlight runs pre-flight checks for a migration job and updates the DB.
func (o *Orchestrator) RunPreFlight(ctx context.Context, jobID uuid.UUID) (*PreFlightReport, error) {
	job, err := o.queries.GetMigrationJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("get migration job: %w", err)
	}

	// Update status to checking.
	if err := o.queries.UpdateMigrationJobStatus(ctx, db.UpdateMigrationJobStatusParams{
		ID:     jobID,
		Status: StatusChecking,
	}); err != nil {
		return nil, fmt.Errorf("update job status: %w", err)
	}

	srcClient, _, err := o.clientForCluster(ctx, job.SourceClusterID)
	if err != nil {
		return nil, fmt.Errorf("source cluster client: %w", err)
	}

	var tgtClient *proxmox.Client
	if job.MigrationType == TypeCrossCluster {
		tgtClient, _, err = o.clientForCluster(ctx, job.TargetClusterID)
		if err != nil {
			return nil, fmt.Errorf("target cluster client: %w", err)
		}
	} else {
		tgtClient = srcClient
	}

	var storageMap StorageMapping
	var networkMap NetworkMapping
	_ = json.Unmarshal(job.StorageMap, &storageMap)
	_ = json.Unmarshal(job.NetworkMap, &networkMap)

	report, err := RunPreFlightChecks(
		ctx, srcClient, tgtClient,
		job.SourceNode, job.TargetNode,
		int(job.Vmid), job.VmType, job.MigrationType,
		storageMap, networkMap, int(job.TargetVmid),
	)
	if err != nil {
		return nil, fmt.Errorf("run pre-flight checks: %w", err)
	}

	// Save results to DB.
	reportJSON, _ := json.Marshal(report)
	status := StatusPending
	if !report.Passed {
		status = StatusFailed
	}

	if err := o.queries.UpdateMigrationJobChecks(ctx, db.UpdateMigrationJobChecksParams{
		ID:           jobID,
		CheckResults: reportJSON,
		Status:       status,
	}); err != nil {
		return nil, fmt.Errorf("save check results: %w", err)
	}

	return report, nil
}

// Execute runs the migration for a job. This is intended to be called in a goroutine.
func (o *Orchestrator) Execute(ctx context.Context, jobID uuid.UUID, userID uuid.UUID) {
	job, err := o.queries.GetMigrationJob(ctx, jobID)
	if err != nil {
		o.failJob(ctx, jobID, fmt.Sprintf("get migration job: %v", err), nil)
		return
	}

	mc := o.resolveMigrationContext(ctx, job, userID)

	srcClient, srcCluster, err := o.clientForCluster(ctx, job.SourceClusterID)
	if err != nil {
		o.failJob(ctx, jobID, fmt.Sprintf("source cluster client: %v", err), &mc)
		return
	}

	// For storage-only migration, use a different execution path that
	// moves each disk individually and tracks progress.
	if job.MigrationType == TypeIntraCluster && job.MigrationMode == ModeStorage {
		o.executeStorageMigration(ctx, srcClient, job, jobID, userID, &mc)
		return
	}

	// For "both" mode, do live migration first, then storage migration after.
	if job.MigrationType == TypeIntraCluster && job.MigrationMode == ModeBoth {
		o.executeBothMigration(ctx, srcClient, srcCluster, job, jobID, userID, &mc)
		return
	}

	var upid string

	switch job.MigrationType {
	case TypeIntraCluster:
		upid, err = o.executeIntraCluster(ctx, srcClient, job)
	case TypeCrossCluster:
		upid, err = o.executeCrossCluster(ctx, srcClient, srcCluster, job, &mc)
	default:
		o.failJob(ctx, jobID, fmt.Sprintf("unknown migration type: %s", job.MigrationType), &mc)
		return
	}

	if err != nil {
		o.failJob(ctx, jobID, err.Error(), &mc)
		return
	}

	o.startAndPollMigration(ctx, srcClient, job, jobID, userID, upid, &mc)
}

// startAndPollMigration marks the job as migrating, inserts task history, and polls.
func (o *Orchestrator) startAndPollMigration(ctx context.Context, client *proxmox.Client, job db.MigrationJob, jobID, userID uuid.UUID, upid string, mc *migrationContext) {
	// Mark as migrating with UPID.
	if err := o.queries.SetMigrationJobStarted(ctx, db.SetMigrationJobStartedParams{
		ID:        jobID,
		StartedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Upid:      upid,
	}); err != nil {
		o.logger.Error("failed to set job started", "job_id", jobID, "error", err)
	}

	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "migrating")
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskCreated, "task", upid, "migrate")

	// Audit log with UPID so the activity panel can track and double-click to reopen.
	typeLabel := "VM"
	if job.VmType == VMTypeLXC {
		typeLabel = "CT"
	}
	o.auditLog(ctx, mc, "migrate_running",
		fmt.Sprintf(`{"upid":%q,"vmid":%d,"vm_type":%q,"node":%q,"source_node":%q,"target_node":%q,"migration_type":%q}`,
			upid, job.Vmid, typeLabel, job.SourceNode, job.SourceNode, job.TargetNode, job.MigrationType))

	// Insert task history so the task shows in the Tasks panel.
	description := fmt.Sprintf("Migrate %s: %s → %s", mc.vmLabel, job.SourceNode, job.TargetNode)
	_, taskErr := o.queries.InsertTaskHistory(ctx, db.InsertTaskHistoryParams{
		ClusterID:   job.SourceClusterID,
		UserID:      userID,
		Upid:        upid,
		Description: description,
		Status:      "running",
		Node:        job.SourceNode,
		TaskType:    "migrate",
	})
	if taskErr != nil {
		o.logger.Warn("failed to insert task history", "job_id", jobID, "error", taskErr)
	}

	// Poll task status.
	o.pollTaskStatus(ctx, client, job.SourceNode, upid, jobID, mc)
}

func (o *Orchestrator) executeIntraCluster(ctx context.Context, client *proxmox.Client, job db.MigrationJob) (string, error) {
	params := proxmox.MigrateParams{
		Target: job.TargetNode,
		Online: job.Online,
	}
	// 0 means "no limit set" — leave it off the request so PVE applies the
	// datacenter/storage default, same as the cross-cluster path.
	if job.BwlimitKib > 0 {
		params.BWLimit = int(job.BwlimitKib)
	}

	switch job.VmType {
	case VMTypeQEMU:
		return client.MigrateVM(ctx, job.SourceNode, int(job.Vmid), params)
	case VMTypeLXC:
		return client.MigrateCT(ctx, job.SourceNode, int(job.Vmid), params)
	default:
		return "", fmt.Errorf("unsupported VM type: %s", job.VmType)
	}
}

// executeStorageMigration moves all disks of a VM to a target storage on the same node.
// Each disk move creates a real Proxmox task with its own UPID, so the task panel
// shows real progress and logs — identical to moving a disk from the hardware tab.
func (o *Orchestrator) executeStorageMigration(ctx context.Context, client *proxmox.Client, job db.MigrationJob, jobID, _ uuid.UUID, mc *migrationContext) {
	disks, err := discoverDisks(ctx, client, job.SourceNode, int(job.Vmid), job.VmType)
	if err != nil {
		o.failJob(ctx, jobID, fmt.Sprintf("failed to discover disks: %v", err), mc)
		return
	}

	if len(disks) == 0 {
		o.failJob(ctx, jobID, "no movable disks found on VM", mc)
		return
	}

	// storage_map (keyed by disk key) selects a per-disk target; an empty map
	// routes every disk to the single target_storage. Disks already on their
	// target are skipped.
	storageMap := map[string]string{}
	_ = json.Unmarshal(job.StorageMap, &storageMap)
	moves := resolveDiskTargets(disks, storageMap, job.TargetStorage)
	if len(moves) == 0 {
		o.failJob(ctx, jobID, "no disks to move — every selected disk is already on its target storage", mc)
		return
	}

	o.logger.Info("starting storage migration", "job_id", jobID, "disks", len(moves))

	// Use the first disk move's UPID as the migration job UPID (updated below).
	var firstUpid string

	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "migrating")

	for i, m := range moves {
		spec := diskMoveSpec(job, m)
		var upid string
		switch job.VmType {
		case VMTypeQEMU:
			upid, err = client.MoveDisk(ctx, job.SourceNode, int(job.Vmid), spec.VMParams())
		case VMTypeLXC:
			upid, err = client.MoveCTVolume(ctx, job.SourceNode, int(job.Vmid), spec.CTParams())
		default:
			o.failJob(ctx, jobID, fmt.Sprintf("unsupported VM type: %s", job.VmType), mc)
			return
		}

		if err != nil {
			o.failJob(ctx, jobID, fmt.Sprintf("failed to move disk %s: %v", m.Disk, err), mc)
			return
		}

		if i == 0 {
			firstUpid = upid
		}

		o.recordDiskMove(ctx, mc, job.SourceNode, spec, upid, i+1, len(moves))

		// Update migration job progress and current UPID.
		progress := float64(i) / float64(len(moves))
		_ = o.queries.UpdateMigrationJobProgress(ctx, db.UpdateMigrationJobProgressParams{
			ID:       jobID,
			Progress: progress,
			Upid:     upid,
		})

		// Mark the migration job as started on the first disk.
		if i == 0 {
			_ = o.queries.SetMigrationJobStarted(ctx, db.SetMigrationJobStartedParams{
				ID:        jobID,
				StartedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
				Upid:      upid,
			})
		}

		o.logger.Info("moving disk", "job_id", jobID, "disk", m.Disk, "target", m.Target, "upid", upid)

		// Poll this disk move task until completion.
		// RunningTaskUpdater on the frontend also polls, but we need to
		// wait here before starting the next disk.
		if err := o.waitForTask(ctx, client, job.SourceNode, upid); err != nil {
			o.failJob(ctx, jobID, fmt.Sprintf("disk %s move failed: %v", m.Disk, err), mc)
			return
		}
	}

	// All disks moved successfully.
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
		ID:          jobID,
		Status:      StatusCompleted,
		CompletedAt: now,
	})
	o.logger.Info("storage migration completed", "job_id", jobID, "disks_moved", len(disks))
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "completed")
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskUpdate, "task", firstUpid, "completed")
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindVMStateChange, "vm", mc.vmDBID, "storage_migrated")
}

// executeBothMigration does a live migration first, then a storage migration.
func (o *Orchestrator) executeBothMigration(ctx context.Context, client *proxmox.Client, _ db.Cluster, job db.MigrationJob, jobID, userID uuid.UUID, mc *migrationContext) {
	// Phase 1: Live migration to target node.
	upid, err := o.executeIntraCluster(ctx, client, job)
	if err != nil {
		o.failJob(ctx, jobID, fmt.Sprintf("live migration phase failed: %v", err), mc)
		return
	}

	// Mark as migrating and poll live migration.
	if err := o.queries.SetMigrationJobStarted(ctx, db.SetMigrationJobStartedParams{
		ID:        jobID,
		StartedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Upid:      upid,
	}); err != nil {
		o.logger.Error("failed to set job started", "job_id", jobID, "error", err)
	}
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "migrating")
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskCreated, "task", upid, "migrate")

	// Audit log with UPID so the activity panel can track the live phase.
	typeLabel := "VM"
	if job.VmType == VMTypeLXC {
		typeLabel = "CT"
	}
	o.auditLog(ctx, mc, "migrate_running",
		fmt.Sprintf(`{"upid":%q,"vmid":%d,"vm_type":%q,"node":%q,"source_node":%q,"target_node":%q,"migration_type":"both"}`,
			upid, job.Vmid, typeLabel, job.SourceNode, job.SourceNode, job.TargetNode))

	description := fmt.Sprintf("Migrate %s: %s → %s (phase 1: live)", mc.vmLabel, job.SourceNode, job.TargetNode)
	_, _ = o.queries.InsertTaskHistory(ctx, db.InsertTaskHistoryParams{
		ClusterID:   job.SourceClusterID,
		UserID:      userID,
		Upid:        upid,
		Description: description,
		Status:      "running",
		Node:        job.SourceNode,
		TaskType:    "migrate",
	})

	// Wait for live migration to complete.
	if err := o.waitForTask(ctx, client, job.SourceNode, upid); err != nil {
		// Finalize the task history row as failed. ReconcileTaskHistory is
		// guarded on status='running', so it won't clobber a row the collector
		// reconciler already finalized.
		//
		// err.Error() is UNSCRUBBED, and deliberately so: waitForTask wraps
		// status.ExitStatus verbatim, but this function is reachable only
		// through Execute's `TypeIntraCluster && ModeBoth` gate, so no
		// target-endpoint was ever assembled and there is no credential in
		// play — mc.scrubber is nil here by construction. That is the ONLY
		// reason it is safe. Adding a cross-cluster path to ModeBoth means
		// wrapping this in mc.scrub first; see the writer inventory in
		// pollTaskStatus.
		now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
		_, _ = o.queries.ReconcileTaskHistory(ctx, db.ReconcileTaskHistoryParams{
			Upid:       upid,
			Status:     "failed",
			ExitStatus: err.Error(),
			FinishedAt: now,
		})
		o.failJob(ctx, jobID, fmt.Sprintf("live migration phase failed: %v", err), mc)
		return
	}

	// Mark live migration task as complete (guarded on status='running').
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	_, _ = o.queries.ReconcileTaskHistory(ctx, db.ReconcileTaskHistoryParams{
		Upid:       upid,
		Status:     "completed",
		ExitStatus: "OK",
		FinishedAt: now,
	})
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskUpdate, "task", upid, "completed")

	o.logger.Info("live migration phase completed, starting storage migration", "job_id", jobID)

	// Phase 2: Storage migration on the target node (VM is now on target_node).
	_ = o.queries.UpdateMigrationJobProgress(ctx, db.UpdateMigrationJobProgressParams{
		ID:       jobID,
		Progress: 0.5,
		Upid:     upid,
	})

	disks, err := discoverDisks(ctx, client, job.TargetNode, int(job.Vmid), job.VmType)
	if err != nil {
		o.failJob(ctx, jobID, fmt.Sprintf("failed to discover disks on target node: %v", err), mc)
		return
	}

	// storage_map (keyed by disk key) selects per-disk targets; an empty map
	// routes every disk to target_storage. After the live phase disks may
	// already sit on their target (shared storage), so a no-op set means the
	// migration is already done.
	storageMap := map[string]string{}
	_ = json.Unmarshal(job.StorageMap, &storageMap)
	moves := resolveDiskTargets(disks, storageMap, job.TargetStorage)
	if len(moves) == 0 {
		// Nothing left to move — the live phase already finished the migration.
		_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
			ID:          jobID,
			Status:      StatusCompleted,
			CompletedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "completed")
		o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindInventoryChange, "vm", mc.vmDBID, "migrated")
		return
	}

	var lastDiskUpid string
	for i, m := range moves {
		spec := diskMoveSpec(job, m)
		var diskUpid string
		switch job.VmType {
		case VMTypeQEMU:
			diskUpid, err = client.MoveDisk(ctx, job.TargetNode, int(job.Vmid), spec.VMParams())
		case VMTypeLXC:
			diskUpid, err = client.MoveCTVolume(ctx, job.TargetNode, int(job.Vmid), spec.CTParams())
		}

		if err != nil {
			o.failJob(ctx, jobID, fmt.Sprintf("failed to move disk %s on target node: %v", m.Disk, err), mc)
			return
		}

		lastDiskUpid = diskUpid

		o.recordDiskMove(ctx, mc, job.TargetNode, spec, diskUpid, i+1, len(moves))

		progress := 0.5 + (float64(i)/float64(len(moves)))*0.5
		_ = o.queries.UpdateMigrationJobProgress(ctx, db.UpdateMigrationJobProgressParams{
			ID:       jobID,
			Progress: progress,
			Upid:     diskUpid,
		})

		if waitErr := o.waitForTask(ctx, client, job.TargetNode, diskUpid); waitErr != nil {
			o.failJob(ctx, jobID, fmt.Sprintf("disk %s move failed on target node: %v", m.Disk, waitErr), mc)
			return
		}
	}

	// Both phases completed.
	_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
		ID:          jobID,
		Status:      StatusCompleted,
		CompletedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	o.logger.Info("both migration completed", "job_id", jobID)
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "completed")
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskUpdate, "task", lastDiskUpid, "completed")
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindInventoryChange, "vm", mc.vmDBID, "migrated")
}

// waitForTask polls a Proxmox task until it completes and returns an error if it failed.
func (o *Orchestrator) waitForTask(ctx context.Context, client *proxmox.Client, node, upid string) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled")
		case <-ticker.C:
			status, err := client.GetTaskStatus(ctx, node, upid)
			if err != nil {
				o.logger.Warn("failed to poll task", "upid", upid, "error", err)
				continue
			}
			if status.Status == "running" {
				continue
			}
			if proxmox.TaskSucceeded(status.ExitStatus) {
				return nil
			}
			return fmt.Errorf("task exit status: %s", status.ExitStatus)
		}
	}
}

// movableDisk is a VM/CT disk/volume that storage migration can move, paired
// with the storage it currently lives on.
type movableDisk struct {
	Key     string
	Storage string
}

// diskMove is a resolved per-disk storage-migration target.
type diskMove struct {
	Disk   string
	Target string
}

// volumeStorage extracts the storage name from a disk/volume config value such
// as "local-lvm:vm-100-disk-0,size=32G" -> "local-lvm".
func volumeStorage(val string) string {
	first := val
	if i := strings.IndexByte(first, ','); i >= 0 {
		first = first[:i]
	}
	if i := strings.IndexByte(first, ':'); i >= 0 {
		return first[:i]
	}
	return ""
}

// diskMoveSpec turns one planned move into the same spec the per-disk API
// endpoints build, so a disk moved by a migration job and one moved from the
// hardware tab go through identical rules: delete_source off keeps the
// original as an unusedN entry, bwlimit 0 means the storage default, and the
// format is dropped for containers.
func diskMoveSpec(job db.MigrationJob, m diskMove) proxmox.DiskMoveSpec {
	return proxmox.DiskMoveSpec{
		Disk:          m.Disk,
		TargetStorage: m.Target,
		Format:        job.DiskFormat,
		DeleteSource:  job.DeleteSource,
		BWLimitKiB:    int(job.BwlimitKib),
	}
}

// resolveDiskTargets decides where each discovered disk should move for a
// storage migration. A non-empty storageMap (keyed by disk key) selects a
// target per disk; disks absent from the map stay put ("keep in place"). An
// empty storageMap routes every disk to the single fallback target. Disks
// already on their resolved target are skipped — a same-storage move is a
// no-op that Proxmox rejects.
func resolveDiskTargets(disks []movableDisk, storageMap map[string]string, fallback string) []diskMove {
	perDisk := len(storageMap) > 0
	var moves []diskMove
	for _, d := range disks {
		target := fallback
		if perDisk {
			target = storageMap[d.Key]
		}
		if target == "" || target == d.Storage {
			continue
		}
		moves = append(moves, diskMove{Disk: d.Key, Target: target})
	}
	return moves
}

// discoverDisks returns the movable disks/volumes of a VM/CT, each with the
// storage it currently lives on.
func discoverDisks(ctx context.Context, client *proxmox.Client, node string, vmid int, vmType string) ([]movableDisk, error) {
	var cfg map[string]interface{}
	var err error

	switch vmType {
	case VMTypeQEMU:
		cfg, err = client.GetVMConfig(ctx, node, vmid)
	case VMTypeLXC:
		cfg, err = client.GetCTConfig(ctx, node, vmid)
	default:
		return nil, fmt.Errorf("unsupported VM type: %s", vmType)
	}
	if err != nil {
		return nil, err
	}

	var disks []movableDisk

	if vmType == VMTypeQEMU {
		// QEMU disks: scsi*, virtio*, sata*, ide* (but not cdrom/cloudinit)
		for key, val := range cfg {
			if !isQEMUDiskKey(key) {
				continue
			}
			s, ok := val.(string)
			if !ok {
				continue
			}
			// Skip cdrom entries (media=cdrom) and cloudinit drives
			if strings.Contains(s, "media=cdrom") || strings.Contains(s, "cloudinit") {
				continue
			}
			// Skip EFI disk (efidisk0)
			if key == "efidisk0" {
				continue
			}
			// Must reference a storage (contains ":" like "local-lvm:vm-100-disk-0")
			if !strings.Contains(s, ":") {
				continue
			}
			disks = append(disks, movableDisk{Key: key, Storage: volumeStorage(s)})
		}
	} else {
		// LXC volumes: rootfs, mp0, mp1, etc.
		for key, val := range cfg {
			if key == "rootfs" || strings.HasPrefix(key, "mp") {
				s, ok := val.(string)
				if !ok {
					continue
				}
				if !strings.Contains(s, ":") {
					continue
				}
				disks = append(disks, movableDisk{Key: key, Storage: volumeStorage(s)})
			}
		}
	}

	return disks, nil
}

// isQEMUDiskKey checks if a config key is a QEMU disk key.
func isQEMUDiskKey(key string) bool {
	prefixes := []string{"scsi", "virtio", "sata", "ide", "efidisk"}
	for _, p := range prefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

func (o *Orchestrator) executeCrossCluster(ctx context.Context, srcClient *proxmox.Client, _ db.Cluster, job db.MigrationJob, mc *migrationContext) (string, error) {
	_, tgtCluster, err := o.clientForCluster(ctx, job.TargetClusterID)
	if err != nil {
		return "", fmt.Errorf("target cluster client: %w", err)
	}

	tgtTokenSecret, err := crypto.Decrypt(tgtCluster.TokenSecretEncrypted, o.encryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt target credentials: %w", err)
	}

	// Proxmox expects just the hostname/IP — port 8006 is the default.
	targetHost := tgtCluster.ApiUrl
	if u, err := url.Parse(tgtCluster.ApiUrl); err == nil && u.Hostname() != "" {
		targetHost = u.Hostname()
	}

	endpoint := proxmox.TargetEndpoint{
		Host:        targetHost,
		APIToken:    fmt.Sprintf("%s=%s", tgtCluster.TokenID, tgtTokenSecret),
		Fingerprint: tgtCluster.TlsFingerprint,
	}

	// The credential now exists, and from here it can come back inside a
	// Proxmox failure by two routes: synchronously, as the error this
	// function returns, and asynchronously, as the worker's exit status that
	// pollTaskStatus reads minutes later. Arming the context here covers the
	// second one at the point the first is created, so the two cannot drift.
	mc.armEndpointScrubber(endpoint.APIToken)

	var storageMap StorageMapping
	var networkMap NetworkMapping
	_ = json.Unmarshal(job.StorageMap, &storageMap)
	_ = json.Unmarshal(job.NetworkMap, &networkMap)

	// Auto-detect bridge mapping from VM config when none provided.
	// Proxmox requires target-bridge even for same-name bridges.
	if len(networkMap) == 0 {
		bridges := detectBridges(ctx, srcClient, job.SourceNode, int(job.Vmid), job.VmType)
		if len(bridges) > 0 {
			networkMap = make(NetworkMapping, len(bridges))
			for _, b := range bridges {
				networkMap[b] = b // map each bridge to itself on the target
			}
		}
	}

	bridgeMapping := formatMapping(networkMap)

	// Auto-pick a free VMID on the target cluster when none specified,
	// since Proxmox reuses the source VMID which may already exist.
	targetVMID := int(job.TargetVmid)
	if targetVMID <= 0 {
		tgtClient, _, err := o.clientForCluster(ctx, job.TargetClusterID)
		if err == nil {
			targetVMID = findFreeVMID(ctx, tgtClient, int(job.Vmid))
		}
	}

	var upid string
	switch job.VmType {
	case VMTypeQEMU:
		params := proxmox.RemoteMigrateVMParams{
			TargetEndpoint: endpoint,
			TargetBridge:   bridgeMapping,
			Online:         job.Online,
			Delete:         job.DeleteSource,
		}
		if targetVMID > 0 {
			params.TargetVMID = targetVMID
		}
		if job.BwlimitKib > 0 {
			params.BWLimit = int(job.BwlimitKib)
		}
		if len(storageMap) > 0 {
			params.TargetStorage = formatMapping(storageMap)
		}
		upid, err = srcClient.RemoteMigrateVM(ctx, job.SourceNode, int(job.Vmid), params)

	case VMTypeLXC:
		params := proxmox.RemoteMigrateCTParams{
			TargetEndpoint: endpoint,
			TargetBridge:   bridgeMapping,
			Restart:        job.Online,
			Delete:         job.DeleteSource,
		}
		if targetVMID > 0 {
			params.TargetVMID = targetVMID
		}
		if job.BwlimitKib > 0 {
			params.BWLimit = int(job.BwlimitKib)
		}
		if len(storageMap) > 0 {
			params.TargetStorage = formatMapping(storageMap)
		}
		upid, err = srcClient.RemoteMigrateCT(ctx, job.SourceNode, int(job.Vmid), params)

	default:
		return "", fmt.Errorf("unsupported VM type: %s", job.VmType)
	}

	// Both guest branches fall through to one exit rather than returning, so
	// the scrub is written once. This DISCOURAGES a new branch from skipping
	// it; it does not prevent one — a third case that returns directly still
	// compiles, and the default arm three lines up already returns unscrubbed.
	// That one is correct, but NOT because it predates the credential (the
	// token was assembled ~70 lines earlier and the context is already armed):
	// its message is "unsupported VM type: " + job.VmType, which structurally
	// cannot carry the secret.
	// Nothing enforces the shape, so a new guest type needs its own test, the
	// way TestExecuteCrossCluster_ScrubsTheTargetTokenFromTheFailure covers
	// the two that exist.
	if err != nil {
		return "", scrubEndpointSecret(err, endpoint.APIToken)
	}
	return upid, nil
}

// redactedTokenSecret is what stands in for the target cluster's token secret
// once it has been scrubbed out of an error.
const redactedTokenSecret = "REDACTED"

// scrubbedError carries an error whose text has had a credential removed.
//
// Only Error() is overridden, because Error() is the leak path, and it has two
// Viewer-readable sinks rather than one. Execute hands executeCrossCluster's
// err.Error() to failJob, which logs it, writes it to
// migration_jobs.error_message — served back as error_message by the migration
// handlers, gated on view:migration on the job's source or target cluster —
// and embeds the same string in the audit row's details JSON, which is
// readable with view:audit, granted to every Viewer by default.
//
// Unwrap is kept so errors.Is sentinel checks (proxmox.ErrNotFound,
// proxmox.ErrForbidden) still see through the scrub. The cost of keeping it:
// errors.As still reaches the underlying *proxmox.APIError, whose .Message and
// .Fields hold the unscrubbed body. So this error must never be handed to
// handlers.mapProxmoxError, which returns apiErr.Message to the client
// verbatim. failJob's err.Error() is its only sink today, and Execute is
// fire-and-forget, so there is no path there now — but making cross-cluster
// migration synchronous and reaching for the house error-mapper would undo
// this silently.
type scrubbedError struct {
	msg string
	err error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }

// scrubEndpointSecret removes the target cluster's API token secret from err's
// message.
//
// Why here and not in the Proxmox client: APIError.Message carries Proxmox's
// own words — the raw response body when the {"errors":{…}} envelope does not
// parse, and the flattened "field: msg" pairs when it does — and either shape
// can contain a value the vendor chose to echo. The rest of the client reads
// that text. Scrubbing centrally would mean teaching the client about a
// credential that only this one call site holds, and would change error text
// everything else depends on. So the scrub sits at the narrow end — the single
// function that both assembles the credential and owns the error on its way to
// a persisted, Viewer-readable column.
//
// Scope, stated honestly: this is a STRUCTURAL exposure, not a demonstrated
// leak. PVE's JSONSchema rejections generally name the parameter and the rule
// rather than the value, and nothing here has observed PVE echoing a rejected
// target-endpoint back. What is true is that the endpoint's property string
// carries a stored, long-lived API token, that the whole response body reaches
// APIError.Message untouched, and that the only thing standing between the two
// is an upstream vendor's error-formatting choice we neither control nor
// re-check on upgrade.
//
// It scrubs the SECRET HALF rather than the whole "<tokenid>=<secret>" pair.
// The secret is the part that authenticates, and it is the narrower needle:
// every echo shape containing the full pair also contains the secret, while a
// reformatted or partial echo may contain only the secret. Leaving the token
// id readable also keeps the failure diagnosable, and it is already visible in
// the clusters API.
func scrubEndpointSecret(err error, apiToken string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	// No empty-secret check here on purpose. scrubSecret owns that guard, and
	// a second copy of it would make both unkillable: each would mask the
	// other, so no test could tell either one from a no-op. An empty secret
	// returns msg unchanged, which falls through to the identity return.
	scrubbed := scrubSecret(msg, endpointTokenSecret(apiToken))
	if scrubbed == msg {
		return err
	}
	return &scrubbedError{msg: scrubbed, err: err}
}

// scrubSecret replaces every occurrence of secret in s. The empty secret is
// the vacuous case and must short-circuit: strings.ReplaceAll with an empty
// needle splices the replacement in at every position, mangling the text —
// and there is nothing to hide in the first place.
func scrubSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, redactedTokenSecret)
}

// endpointTokenSecret splits a proxmox.TargetEndpoint.APIToken — built as
// "<tokenid>=<secret>" at the top of executeCrossCluster — into the half that
// authenticates. A value with no "=" is treated as secret in full, which is
// the safe reading: the only reason to be looking at this string is that it is
// credential material.
func endpointTokenSecret(apiToken string) string {
	if _, secret, ok := strings.Cut(apiToken, "="); ok {
		return secret
	}
	return apiToken
}

// abandonTaskRow finalizes a task_history row with a CONSTANT exit status, for
// the two exits where this loop gives up without PVE having told it anything.
//
// One implementation for both, because there is nothing to vary: a constant
// needs no scrub, which is the whole reason these exits are safe to write at
// all. PVE's text is what cannot be persisted unscrubbed; "interrupted" and
// "vanished" carry none of it.
//
// Writes through cleanupCtxFor so a cancelled parent does not silently turn the
// write into a no-op — which is exactly the orphan these exits exist to
// prevent, and which pgx would do without a live context.
func (o *Orchestrator) abandonTaskRow(ctx context.Context, upid, exitStatus string) {
	dbCtx, cancel := cleanupCtxFor(ctx)
	defer cancel()
	_, _ = o.queries.ReconcileTaskHistory(dbCtx, db.ReconcileTaskHistoryParams{
		Upid:       upid,
		Status:     "failed",
		ExitStatus: exitStatus,
		FinishedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
}

// pollTaskStatus watches a dispatched Proxmox task to completion and finalizes
// both the migration job and its task_history row.
//
// It has to finalize that row on EVERY exit, because for a cross-cluster
// migration it is the only thing left that can: ListRunningTaskHistoryByCluster
// withholds the row from the collector's stale-task sweep (the credential
// boundary — see queries/tasks.sql), and DeleteCompletedTasks never prunes a
// row still marked running. Returning without a terminal write leaves the
// Tasks page showing "Running" forever.
func (o *Orchestrator) pollTaskStatus(ctx context.Context, client *proxmox.Client, node, upid string, jobID uuid.UUID, mc *migrationContext) {
	ticker := time.NewTicker(o.pollInterval)
	defer ticker.Stop()

	// When polling started failing, zeroed by any successful poll. Bounds the
	// loop on how long Proxmox has been unable to answer, not on how long the
	// migration has taken — see defaultPollStaleGrace.
	var unreachableSince time.Time

	for {
		select {
		case <-ctx.Done():
			// failJob writes migration_jobs through cleanupCtxFor, so a SIGTERM
			// mid-migration leaves the JOB row correct on its own; the task row
			// is this call's job.
			//
			// Note what this costs, because nothing corrects it: PVE will most
			// likely finish the worker, but the row now says failed, the
			// anti-join withholds it from the reconciler, and
			// ReconcileTaskHistory is guarded on status='running' — so no
			// later sync can revise it. A permanently-wrong terminal state is
			// the deliberate trade against a row stuck at Running forever, and
			// failJob already did the same to the job row before this existed.
			o.abandonTaskRow(ctx, upid, "interrupted")
			o.failJob(ctx, jobID, "context cancelled", mc)
			return
		case <-ticker.C:
			status, err := client.GetTaskStatus(ctx, node, upid)
			if err != nil {
				o.logger.Warn("failed to poll task status", "job_id", jobID, "error", err)
				// A node that rebooted, or a task log that rotated, never
				// starts answering again — without a bound this goroutine
				// polls every 5s forever and the row never leaves "running".
				// No process death required, which is why this is bounded
				// rather than documented as a residual.
				if unreachableSince.IsZero() {
					unreachableSince = time.Now()
				} else if time.Since(unreachableSince) > o.pollStaleGrace {
					o.logger.Error("giving up polling task status",
						"job_id", jobID, "upid", upid,
						"unreachable_for", time.Since(unreachableSince))
					o.abandonTaskRow(ctx, upid, "vanished")
					o.failJob(ctx, jobID, "task status unavailable past the stale grace", mc)
					return
				}
				continue
			}
			unreachableSince = time.Time{}

			if status.Status == "running" {
				// Update progress (Proxmox doesn't give %, so we just note it's running).
				_ = o.queries.UpdateMigrationJobProgress(ctx, db.UpdateMigrationJobProgressParams{
					ID:       jobID,
					Progress: 0.5, // Indeterminate - task is running.
					Upid:     upid,
				})
				continue
			}

			// Task completed — update both migration job and task history.
			now := pgtype.Timestamptz{Time: time.Now(), Valid: true}

			// Update task history record (guarded on status='running' so it
			// won't clobber a row the collector reconciler already finalized).
			taskStatus := "completed"
			if !proxmox.TaskSucceeded(status.ExitStatus) {
				taskStatus = "failed"
			}
			// taskStatus above is decided on the raw exit status; what gets
			// STORED is scrubbed. Deliberately narrow: this keeps the
			// PERSISTED copy clean, and does not keep the text from a
			// view:task holder, which it cannot.
			//
			// The reason is outside this function. GET
			// /clusters/:id/tasks/:upid and its /log sibling
			// (registry_vms.go) serve PVE's own exit status and task log
			// straight through at view:task, so the raw text is readable
			// live for as long as Proxmox retains it. That is a decided
			// trade, argued in those two declarations, not an oversight
			// this scrub was meant to cover.
			//
			// On the reconcile path this is the only writer of PVE's text
			// for the row: ListRunningTaskHistoryByCluster withholds a
			// cross-cluster migration's row from the collector reconciler,
			// which would otherwise call the same ReconcileTaskHistory with
			// an UNSCRUBBED exit status and, on a first-write-wins race,
			// leave this call updating nothing. Do not remove that filter on
			// the grounds that this line already scrubs — the two are one
			// mechanism.
			//
			// The collector's other writer of PVE's text is closed too, by a
			// separate guard: ingestTask would persist the same die message
			// at INSERT if a sync tick listed this node in the few
			// statements between the remote_migrate POST returning and the
			// audit/task rows landing, so seenTaskUPIDs withholds any UPID
			// named by a cross-cluster migration_jobs row
			// (ListCrossClusterMigrationUPIDs). Separate guard, not a second
			// copy: that one covers an INSERT on the ingest path, the
			// anti-join a SELECT on the reconcile path, and each is killable
			// without the other.
			//
			// The full set of writers to task_history.exit_status, since a
			// partial inventory here is worse than none — someone extending
			// one of these will read it:
			//
			//  1. this line — scrubbed.
			//  2. collector finalizeTask — raw PVE text; the anti-join keeps
			//     cross-cluster rows out of its listing.
			//  3. collector ingestTask — raw PVE text at INSERT; the
			//     ListCrossClusterMigrationUPIDs lookup withholds the UPID.
			//  4. executeBothMigration, ~570 lines up — writes err.Error()
			//     from waitForTask, which wraps status.ExitStatus verbatim and
			//     does NOT scrub. Safe only because that function is reachable
			//     solely through the TypeIntraCluster && ModeBoth gate in
			//     Execute, so no target-endpoint was ever assembled and
			//     mc.scrubber is nil. Give ModeBoth a cross-cluster path and
			//     that call needs mc.scrub before anything else changes.
			//  5. internal/drs/executor.go — writes waitForTask's exitStatus
			//     verbatim. Safe only because DRS issues MigrateVM/MigrateCT,
			//     never RemoteMigrate, and creates no migration_jobs rows at
			//     all — which also means neither guard above would cover it.
			//     Pointing DRS at a remote target needs both.
			//  6. PUT /api/v1/tasks/:upid (manage:task) — no status='running'
			//     guard at all, but it writes the request body rather than
			//     vendor text, so there is nothing there to scrub.
			//  7. executeBothMigration's SUCCESS write, 12 lines below its
			//     failure sibling in 4 — a literal "OK".
			//  8. abandonTaskRow, added by the same change as this comment —
			//     literals "interrupted" and "vanished".
			//
			// 7 and 8 are constants, so they carry nothing to scrub. They are
			// listed because the rule this comment follows is that a PARTIAL
			// inventory is worse than none: a reader checking whether some new
			// write needs mc.scrub has to be able to trust that the set is the
			// set. 8 in particular was introduced by the change that wrote this
			// paragraph, which is how a list like this goes stale on day one.
			//
			// 4 and 5 are structurally safe, not luckily so; neither is
			// defended by a test, because today neither can reach a
			// credential.
			//
			// The persisted copy is worth cleaning because it outlives PVE's
			// task log, and it feeds the audit join and the report digest.
			_, _ = o.queries.ReconcileTaskHistory(ctx, db.ReconcileTaskHistoryParams{
				Upid:       upid,
				Status:     taskStatus,
				ExitStatus: mc.scrub(status.ExitStatus),
				FinishedAt: now,
			})

			job := mc.job

			if proxmox.TaskSucceeded(status.ExitStatus) {
				_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
					ID:          jobID,
					Status:      StatusCompleted,
					CompletedAt: now,
				})
				o.logger.Info("migration completed", "job_id", jobID)
				o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "completed")
				o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskUpdate, "task", upid, "completed")
				o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindInventoryChange, "vm", mc.vmDBID, "migrated")
			} else {
				// PVE's worker die-message, straight from the vendor, on its
				// way to migration_jobs.error_message — the same
				// view:migration column the synchronous rejection lands in.
				errMsg := mc.scrub(fmt.Sprintf("Task exit status: %s", status.ExitStatus))
				_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
					ID:           jobID,
					Status:       StatusFailed,
					CompletedAt:  now,
					ErrorMessage: errMsg,
				})
				o.logger.Error("migration failed", "job_id", jobID, "exit_status", mc.scrub(status.ExitStatus))
				o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "failed")
				o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskUpdate, "task", upid, "failed")
			}
			return
		}
	}
}

func (o *Orchestrator) failJob(ctx context.Context, jobID uuid.UUID, errMsg string, mc *migrationContext) {
	// Scrubbed here as well as at executeCrossCluster's exit, because THIS is
	// the function that writes to all three sinks and a caller reaching it by
	// some other route would otherwise have to remember. Identity for every
	// intra-cluster caller (nil scrubber) and idempotent for the cross-cluster
	// one, so the belt costs nothing. Without it the "covered by construction"
	// claim on armEndpointScrubber is false the moment anyone adds an early
	// `return "", err` between arming and that exit.
	errMsg = mc.scrub(errMsg)
	o.logger.Error("migration job failed", "job_id", jobID, "error", errMsg)
	// If ctx was cancelled (graceful shutdown), the DB write would no-op and
	// the row would orphan in 'migrating' status forever. Use a fresh
	// 5-second cleanup context so the failure outcome lands in the row.
	dbCtx, cancel := cleanupCtxFor(ctx)
	defer cancel()
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	_ = o.queries.CompleteMigrationJob(dbCtx, db.CompleteMigrationJobParams{
		ID:           jobID,
		Status:       StatusFailed,
		CompletedAt:  now,
		ErrorMessage: errMsg,
	})
	if mc != nil {
		job := mc.job
		typeLabel := "VM"
		if job.VmType == VMTypeLXC {
			typeLabel = "CT"
		}
		actionPrefix := "migrate"
		if job.MigrationType == TypeCrossCluster {
			actionPrefix = "cross_cluster_migrate"
		}
		o.auditLog(dbCtx, mc, actionPrefix+"_failed",
			fmt.Sprintf(`{"vmid":%d,"vm_type":%q,"source_node":%q,"target_node":%q,"migration_type":%q,"error":%q}`,
				job.Vmid, typeLabel, job.SourceNode, job.TargetNode, job.MigrationType, errMsg))
	}
}

// auditLog inserts an audit log entry using the VM's DB ID as resource_id
// so the enriched query can resolve the VM name and VMID.
func (o *Orchestrator) auditLog(ctx context.Context, mc *migrationContext, action, detailsJSON string) {
	if mc.userID == uuid.Nil {
		return
	}
	// Use "vm" resource_type with the VM's DB ID so the enriched audit query
	// can JOIN on vms.id and show the VM name / VMID.
	resourceType := "vm"
	resourceID := mc.vmDBID
	if resourceID == "" {
		// Fallback if VM wasn't found in DB — use the job ID.
		resourceType = "migration"
		resourceID = mc.job.ID.String()
	}
	_ = o.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: mc.job.SourceClusterID, Valid: true},
		UserID:       pgtype.UUID{Bytes: mc.userID, Valid: true},
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      json.RawMessage(detailsJSON),
	})
	o.eventPub.ClusterEvent(ctx, mc.job.SourceClusterID.String(), events.KindAuditEntry, resourceType, resourceID, action)
}

// recordDiskMove is the single place that records a disk move for tracking.
// It creates an audit log entry with the UPID (so the activity panel can
// track it and double-click to reopen), inserts a task_history row, and
// publishes events. Both executeStorageMigration and executeBothMigration
// call this instead of duplicating the logic.
func (o *Orchestrator) recordDiskMove(ctx context.Context, mc *migrationContext, node string, spec proxmox.DiskMoveSpec, upid string, index, total int) {
	typeLabel := "VM"
	if mc.job.VmType == VMTypeLXC {
		typeLabel = "CT"
	}

	o.auditLog(ctx, mc, "disk_move_running",
		fmt.Sprintf(`{"upid":%q,"vmid":%d,"vm_type":%q,"node":%q,"disk":%q,"target_storage":%q,"format":%q,"delete_source":%t,"bwlimit_kib":%d,"disk_index":%d,"disk_total":%d}`,
			upid, mc.job.Vmid, typeLabel, node, spec.Disk, spec.TargetStorage, spec.Format, spec.DeleteSource, spec.BWLimitKiB, index, total))

	description := fmt.Sprintf("Move disk %s (%s, %d/%d)", spec.Summary(), mc.vmLabel, index, total)
	_, _ = o.queries.InsertTaskHistory(ctx, db.InsertTaskHistoryParams{
		ClusterID:   mc.job.SourceClusterID,
		UserID:      mc.userID,
		Upid:        upid,
		Description: description,
		Status:      "running",
		Node:        node,
		TaskType:    "move_disk",
	})
	o.eventPub.ClusterEvent(ctx, mc.job.SourceClusterID.String(), events.KindTaskCreated, "task", upid, "move_disk")
}

// Cancel cancels a pending or checking job.
func (o *Orchestrator) Cancel(ctx context.Context, jobID uuid.UUID) error {
	return o.queries.CancelMigrationJob(ctx, jobID)
}

// formatMapping converts a map to Proxmox's "src:tgt,src2:tgt2" format.
func formatMapping(m map[string]string) string {
	pairs := make([]string, 0, len(m))
	for src, tgt := range m {
		pairs = append(pairs, src+":"+tgt)
	}
	return strings.Join(pairs, ",")
}

// findFreeVMID returns a VMID that's available on the target cluster.
// It tries the preferred VMID first, then scans upward from 100.
func findFreeVMID(ctx context.Context, client *proxmox.Client, preferred int) int {
	resources, err := client.GetClusterResources(ctx, "vm")
	if err != nil {
		return 0 // let Proxmox handle it
	}

	used := make(map[int]bool, len(resources))
	for _, r := range resources {
		used[r.VMID] = true
	}

	// Try the preferred VMID first (same as source).
	if preferred > 0 && !used[preferred] {
		return preferred
	}

	// Scan from 100 upward for the next free VMID.
	for id := 100; id < 1000000; id++ {
		if !used[id] {
			return id
		}
	}
	return 0
}

// detectBridges extracts bridge names from a VM/CT config's net* properties.
// Values look like "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1".
func detectBridges(ctx context.Context, client *proxmox.Client, node string, vmid int, vmType string) []string {
	var cfg map[string]interface{}
	var err error

	switch vmType {
	case VMTypeQEMU:
		cfg, err = client.GetVMConfig(ctx, node, vmid)
	case VMTypeLXC:
		cfg, err = client.GetCTConfig(ctx, node, vmid)
	default:
		return nil
	}
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var bridges []string
	for key, val := range cfg {
		if !strings.HasPrefix(key, "net") {
			continue
		}
		s, ok := val.(string)
		if !ok {
			continue
		}
		for _, part := range strings.Split(s, ",") {
			if strings.HasPrefix(part, "bridge=") {
				bridge := strings.TrimPrefix(part, "bridge=")
				if bridge != "" && !seen[bridge] {
					seen[bridge] = true
					bridges = append(bridges, bridge)
				}
			}
		}
	}
	return bridges
}
