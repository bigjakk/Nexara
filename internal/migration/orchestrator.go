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

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// Orchestrator manages migration job execution.
type Orchestrator struct {
	queries       *db.Queries
	encryptionKey string
	logger        *slog.Logger
	eventPub      *events.Publisher
}

// NewOrchestrator creates a new migration orchestrator.
func NewOrchestrator(queries *db.Queries, encryptionKey string, logger *slog.Logger, eventPub *events.Publisher) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{
		queries:       queries,
		encryptionKey: encryptionKey,
		logger:        logger,
		eventPub:      eventPub,
	}
}

// migrationContext holds resolved metadata used for task tracking and audit logging.
type migrationContext struct {
	job       db.MigrationJob
	vmDBID    string // VM's database UUID (for audit log resource linking)
	vmName    string // Human-readable VM name
	vmLabel   string // e.g. "VM 100 (my-vm)" or "CT 200 (my-ct)"
	userID    uuid.UUID
}

// clientForCluster creates a Proxmox client from stored cluster credentials.
func (o *Orchestrator) clientForCluster(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, db.Cluster, error) {
	cluster, err := o.queries.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, cluster, fmt.Errorf("get cluster %s: %w", clusterID, err)
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
		upid, err = o.executeCrossCluster(ctx, srcClient, srcCluster, job)
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
func (o *Orchestrator) executeStorageMigration(ctx context.Context, client *proxmox.Client, job db.MigrationJob, jobID, userID uuid.UUID, mc *migrationContext) {
	targetStorage := job.TargetStorage
	if targetStorage == "" {
		o.failJob(ctx, jobID, "target_storage is required for storage migration", mc)
		return
	}

	disks, err := discoverDisks(ctx, client, job.SourceNode, int(job.Vmid), job.VmType)
	if err != nil {
		o.failJob(ctx, jobID, fmt.Sprintf("failed to discover disks: %v", err), mc)
		return
	}

	if len(disks) == 0 {
		o.failJob(ctx, jobID, "no movable disks found on VM", mc)
		return
	}

	o.logger.Info("starting storage migration", "job_id", jobID, "disks", len(disks), "target_storage", targetStorage)

	// Use the first disk move's UPID as the migration job UPID (updated below).
	var firstUpid string

	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "migrating")

	for i, disk := range disks {
		var upid string
		switch job.VmType {
		case VMTypeQEMU:
			upid, err = client.MoveDisk(ctx, job.SourceNode, int(job.Vmid), proxmox.DiskMoveParams{
				Disk:    disk,
				Storage: targetStorage,
				Delete:  true,
			})
		case VMTypeLXC:
			upid, err = client.MoveCTVolume(ctx, job.SourceNode, int(job.Vmid), proxmox.CTVolumeMoveParams{
				Volume:  disk,
				Storage: targetStorage,
				Delete:  true,
			})
		default:
			o.failJob(ctx, jobID, fmt.Sprintf("unsupported VM type: %s", job.VmType), mc)
			return
		}

		if err != nil {
			o.failJob(ctx, jobID, fmt.Sprintf("failed to move disk %s: %v", disk, err), mc)
			return
		}

		if i == 0 {
			firstUpid = upid
		}

		// Insert a task_history entry with the real Proxmox UPID so the task
		// panel shows progress and logs exactly like a hardware tab disk move.
		description := fmt.Sprintf("Move disk %s → %s (%s, %d/%d)", disk, targetStorage, mc.vmLabel, i+1, len(disks))
		_, taskErr := o.queries.InsertTaskHistory(ctx, db.InsertTaskHistoryParams{
			ClusterID:   job.SourceClusterID,
			UserID:      userID,
			Upid:        upid,
			Description: description,
			Status:      "running",
			Node:        job.SourceNode,
			TaskType:    "move_disk",
		})
		if taskErr != nil {
			o.logger.Warn("failed to insert task history", "job_id", jobID, "disk", disk, "error", taskErr)
		}
		o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskCreated, "task", upid, "move_disk")

		// Update migration job progress and current UPID.
		progress := float64(i) / float64(len(disks))
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

		o.logger.Info("moving disk", "job_id", jobID, "disk", disk, "upid", upid)

		// Poll this disk move task until completion.
		// RunningTaskUpdater on the frontend also polls, but we need to
		// wait here before starting the next disk.
		if err := o.waitForTask(ctx, client, job.SourceNode, upid); err != nil {
			o.failJob(ctx, jobID, fmt.Sprintf("disk %s move failed: %v", disk, err), mc)
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
	o.auditLog(ctx, mc, "storage_migrate",
		fmt.Sprintf(`{"vmid":%d,"vm_type":%q,"node":%q,"target_storage":%q,"disks_moved":%d,"status":"completed"}`,
			job.Vmid, job.VmType, job.SourceNode, targetStorage, len(disks)))
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "completed")
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskUpdate, "task", firstUpid, "completed")
	o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindVMStateChange, "vm", mc.vmDBID, "storage_migrated")
}

// executeBothMigration does a live migration first, then a storage migration.
func (o *Orchestrator) executeBothMigration(ctx context.Context, client *proxmox.Client, srcCluster db.Cluster, job db.MigrationJob, jobID, userID uuid.UUID, mc *migrationContext) {
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
		// Update task history.
		now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
		_ = o.queries.UpdateTaskHistory(ctx, db.UpdateTaskHistoryParams{
			Upid:       upid,
			Status:     "stopped",
			ExitStatus: err.Error(),
			FinishedAt: now,
		})
		o.failJob(ctx, jobID, fmt.Sprintf("live migration phase failed: %v", err), mc)
		return
	}

	// Mark live migration task as complete.
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	_ = o.queries.UpdateTaskHistory(ctx, db.UpdateTaskHistoryParams{
		Upid:       upid,
		Status:     "stopped",
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

	if len(disks) == 0 {
		// No disks to move — that's fine, just complete.
		_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
			ID:          jobID,
			Status:      StatusCompleted,
			CompletedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		o.auditLog(ctx, mc, "migrate_both",
			fmt.Sprintf(`{"vmid":%d,"vm_type":%q,"source_node":%q,"target_node":%q,"target_storage":%q,"status":"completed","note":"no movable disks"}`,
				job.Vmid, job.VmType, job.SourceNode, job.TargetNode, job.TargetStorage))
		o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "completed")
		o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindInventoryChange, "vm", mc.vmDBID, "migrated")
		return
	}

	targetStorage := job.TargetStorage
	var lastDiskUpid string
	for i, disk := range disks {
		var diskUpid string
		switch job.VmType {
		case VMTypeQEMU:
			diskUpid, err = client.MoveDisk(ctx, job.TargetNode, int(job.Vmid), proxmox.DiskMoveParams{
				Disk:    disk,
				Storage: targetStorage,
				Delete:  true,
			})
		case VMTypeLXC:
			diskUpid, err = client.MoveCTVolume(ctx, job.TargetNode, int(job.Vmid), proxmox.CTVolumeMoveParams{
				Volume:  disk,
				Storage: targetStorage,
				Delete:  true,
			})
		}

		if err != nil {
			o.failJob(ctx, jobID, fmt.Sprintf("failed to move disk %s on target node: %v", disk, err), mc)
			return
		}

		lastDiskUpid = diskUpid

		// Insert task_history with real Proxmox UPID for each disk move.
		diskDesc := fmt.Sprintf("Move disk %s → %s (%s, %d/%d)", disk, targetStorage, mc.vmLabel, i+1, len(disks))
		_, _ = o.queries.InsertTaskHistory(ctx, db.InsertTaskHistoryParams{
			ClusterID:   job.SourceClusterID,
			UserID:      userID,
			Upid:        diskUpid,
			Description: diskDesc,
			Status:      "running",
			Node:        job.TargetNode,
			TaskType:    "move_disk",
		})
		o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskCreated, "task", diskUpid, "move_disk")

		progress := 0.5 + (float64(i)/float64(len(disks)))*0.5
		_ = o.queries.UpdateMigrationJobProgress(ctx, db.UpdateMigrationJobProgressParams{
			ID:       jobID,
			Progress: progress,
			Upid:     diskUpid,
		})

		if waitErr := o.waitForTask(ctx, client, job.TargetNode, diskUpid); waitErr != nil {
			o.failJob(ctx, jobID, fmt.Sprintf("disk %s move failed on target node: %v", disk, waitErr), mc)
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
	o.auditLog(ctx, mc, "migrate_both",
		fmt.Sprintf(`{"vmid":%d,"vm_type":%q,"source_node":%q,"target_node":%q,"target_storage":%q,"disks_moved":%d,"status":"completed"}`,
			job.Vmid, job.VmType, job.SourceNode, job.TargetNode, job.TargetStorage, len(disks)))
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
			if migrationSucceeded(status.ExitStatus) {
				return nil
			}
			return fmt.Errorf("task exit status: %s", status.ExitStatus)
		}
	}
}

// discoverDisks returns a list of movable disk/volume keys from a VM/CT config.
func discoverDisks(ctx context.Context, client *proxmox.Client, node string, vmid int, vmType string) ([]string, error) {
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

	var disks []string

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
			disks = append(disks, key)
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
				disks = append(disks, key)
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

func (o *Orchestrator) executeCrossCluster(ctx context.Context, srcClient *proxmox.Client, srcCluster db.Cluster, job db.MigrationJob) (string, error) {
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
		return srcClient.RemoteMigrateVM(ctx, job.SourceNode, int(job.Vmid), params)

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
		return srcClient.RemoteMigrateCT(ctx, job.SourceNode, int(job.Vmid), params)

	default:
		return "", fmt.Errorf("unsupported VM type: %s", job.VmType)
	}
}

func (o *Orchestrator) pollTaskStatus(ctx context.Context, client *proxmox.Client, node, upid string, jobID uuid.UUID, mc *migrationContext) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			o.failJob(ctx, jobID, "context cancelled", mc)
			return
		case <-ticker.C:
			status, err := client.GetTaskStatus(ctx, node, upid)
			if err != nil {
				o.logger.Warn("failed to poll task status", "job_id", jobID, "error", err)
				continue
			}

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

			// Update task history record.
			_ = o.queries.UpdateTaskHistory(ctx, db.UpdateTaskHistoryParams{
				Upid:       upid,
				Status:     "stopped",
				ExitStatus: status.ExitStatus,
				FinishedAt: now,
			})

			job := mc.job
			typeLabel := "VM"
			if job.VmType == VMTypeLXC {
				typeLabel = "CT"
			}

			actionPrefix := "migrate"
			if job.MigrationType == TypeCrossCluster {
				actionPrefix = "cross_cluster_migrate"
			}

			if migrationSucceeded(status.ExitStatus) {
				_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
					ID:          jobID,
					Status:      StatusCompleted,
					CompletedAt: now,
				})
				o.logger.Info("migration completed", "job_id", jobID)
				o.auditLog(ctx, mc, actionPrefix,
					fmt.Sprintf(`{"vmid":%d,"vm_type":%q,"source_node":%q,"target_node":%q,"migration_type":%q,"status":"completed"}`,
						job.Vmid, typeLabel, job.SourceNode, job.TargetNode, job.MigrationType))
				o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "completed")
				o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskUpdate, "task", upid, "completed")
				o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindInventoryChange, "vm", mc.vmDBID, "migrated")
			} else {
				errMsg := fmt.Sprintf("Task exit status: %s", status.ExitStatus)
				_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
					ID:           jobID,
					Status:       StatusFailed,
					CompletedAt:  now,
					ErrorMessage: errMsg,
				})
				o.logger.Error("migration failed", "job_id", jobID, "exit_status", status.ExitStatus)
				o.auditLog(ctx, mc, actionPrefix+"_failed",
					fmt.Sprintf(`{"vmid":%d,"vm_type":%q,"source_node":%q,"target_node":%q,"migration_type":%q,"error":%q}`,
						job.Vmid, typeLabel, job.SourceNode, job.TargetNode, job.MigrationType, status.ExitStatus))
				o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindMigrationUpdate, "migration", jobID.String(), "failed")
				o.eventPub.ClusterEvent(ctx, job.SourceClusterID.String(), events.KindTaskUpdate, "task", upid, "failed")
			}
			return
		}
	}
}

func (o *Orchestrator) failJob(ctx context.Context, jobID uuid.UUID, errMsg string, mc *migrationContext) {
	o.logger.Error("migration job failed", "job_id", jobID, "error", errMsg)
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
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
		o.auditLog(ctx, mc, actionPrefix+"_failed",
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
		UserID:       mc.userID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      json.RawMessage(detailsJSON),
	})
}

// Cancel cancels a pending or checking job.
func (o *Orchestrator) Cancel(ctx context.Context, jobID uuid.UUID) error {
	return o.queries.CancelMigrationJob(ctx, jobID)
}

// migrationSucceeded returns true if a Proxmox task exit status indicates success.
func migrationSucceeded(exitStatus string) bool {
	upper := strings.ToUpper(strings.TrimSpace(exitStatus))
	return upper == "" || upper == "OK" || strings.HasPrefix(upper, "OK ") || upper == "WARNINGS"
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
