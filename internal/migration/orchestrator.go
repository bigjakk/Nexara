package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// Orchestrator manages migration job execution.
type Orchestrator struct {
	queries       *db.Queries
	encryptionKey string
	logger        *slog.Logger
}

// NewOrchestrator creates a new migration orchestrator.
func NewOrchestrator(queries *db.Queries, encryptionKey string, logger *slog.Logger) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{
		queries:       queries,
		encryptionKey: encryptionKey,
		logger:        logger,
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

	// Mark as migrating with UPID.
	if err := o.queries.SetMigrationJobStarted(ctx, db.SetMigrationJobStartedParams{
		ID:        jobID,
		StartedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Upid:      upid,
	}); err != nil {
		o.logger.Error("failed to set job started", "job_id", jobID, "error", err)
	}

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
	o.pollTaskStatus(ctx, srcClient, job.SourceNode, upid, jobID, &mc)
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

func (o *Orchestrator) executeCrossCluster(ctx context.Context, srcClient *proxmox.Client, srcCluster db.Cluster, job db.MigrationJob) (string, error) {
	_, tgtCluster, err := o.clientForCluster(ctx, job.TargetClusterID)
	if err != nil {
		return "", fmt.Errorf("target cluster client: %w", err)
	}

	tgtTokenSecret, err := crypto.Decrypt(tgtCluster.TokenSecretEncrypted, o.encryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt target credentials: %w", err)
	}

	endpoint := proxmox.TargetEndpoint{
		Host:        tgtCluster.ApiUrl,
		APIToken:    fmt.Sprintf("%s=%s", tgtCluster.TokenID, tgtTokenSecret),
		Fingerprint: tgtCluster.TlsFingerprint,
	}

	var storageMap StorageMapping
	var networkMap NetworkMapping
	_ = json.Unmarshal(job.StorageMap, &storageMap)
	_ = json.Unmarshal(job.NetworkMap, &networkMap)

	switch job.VmType {
	case VMTypeQEMU:
		params := proxmox.RemoteMigrateVMParams{
			TargetEndpoint: endpoint,
			Online:         job.Online,
			Delete:         job.DeleteSource,
		}
		if job.TargetVmid > 0 {
			params.TargetVMID = int(job.TargetVmid)
		}
		if job.BwlimitKib > 0 {
			params.BWLimit = int(job.BwlimitKib)
		}
		if len(storageMap) > 0 {
			params.TargetStorage = formatMapping(storageMap)
		}
		if len(networkMap) > 0 {
			params.TargetBridge = formatMapping(networkMap)
		}
		return srcClient.RemoteMigrateVM(ctx, job.SourceNode, int(job.Vmid), params)

	case VMTypeLXC:
		params := proxmox.RemoteMigrateCTParams{
			TargetEndpoint: endpoint,
			Restart:        job.Online,
			Delete:         job.DeleteSource,
		}
		if job.TargetVmid > 0 {
			params.TargetVMID = int(job.TargetVmid)
		}
		if job.BwlimitKib > 0 {
			params.BWLimit = int(job.BwlimitKib)
		}
		if len(storageMap) > 0 {
			params.TargetStorage = formatMapping(storageMap)
		}
		if len(networkMap) > 0 {
			params.TargetBridge = formatMapping(networkMap)
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

			if status.ExitStatus == "OK" {
				_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
					ID:          jobID,
					Status:      StatusCompleted,
					CompletedAt: now,
				})
				o.logger.Info("migration completed", "job_id", jobID)
				o.auditLog(ctx, mc, "migrate",
					fmt.Sprintf(`{"vmid":%d,"vm_type":%q,"source_node":%q,"target_node":%q,"status":"completed"}`,
						job.Vmid, typeLabel, job.SourceNode, job.TargetNode))
			} else {
				errMsg := fmt.Sprintf("Task exit status: %s", status.ExitStatus)
				_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
					ID:           jobID,
					Status:       StatusFailed,
					CompletedAt:  now,
					ErrorMessage: errMsg,
				})
				o.logger.Error("migration failed", "job_id", jobID, "exit_status", status.ExitStatus)
				o.auditLog(ctx, mc, "migrate_failed",
					fmt.Sprintf(`{"vmid":%d,"vm_type":%q,"source_node":%q,"target_node":%q,"error":%q}`,
						job.Vmid, typeLabel, job.SourceNode, job.TargetNode, status.ExitStatus))
			}
			return
		}
	}
}

func (o *Orchestrator) failJob(ctx context.Context, jobID uuid.UUID, errMsg string, mc *migrationContext) {
	o.logger.Error("migration job failed", "job_id", jobID, "error", errMsg)
	_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
		ID:           jobID,
		Status:       StatusFailed,
		CompletedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		ErrorMessage: errMsg,
	})
	if mc != nil {
		job := mc.job
		typeLabel := "VM"
		if job.VmType == VMTypeLXC {
			typeLabel = "CT"
		}
		o.auditLog(ctx, mc, "migrate_failed",
			fmt.Sprintf(`{"vmid":%d,"vm_type":%q,"source_node":%q,"target_node":%q,"error":%q}`,
				job.Vmid, typeLabel, job.SourceNode, job.TargetNode, errMsg))
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
		ClusterID:    mc.job.SourceClusterID,
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

// formatMapping converts a map to Proxmox's "src:tgt,src2:tgt2" format.
func formatMapping(m map[string]string) string {
	pairs := make([]string, 0, len(m))
	for src, tgt := range m {
		pairs = append(pairs, src+":"+tgt)
	}
	return strings.Join(pairs, ",")
}
