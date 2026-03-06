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
func (o *Orchestrator) Execute(ctx context.Context, jobID uuid.UUID) {
	job, err := o.queries.GetMigrationJob(ctx, jobID)
	if err != nil {
		o.failJob(ctx, jobID, fmt.Sprintf("get migration job: %v", err))
		return
	}

	srcClient, srcCluster, err := o.clientForCluster(ctx, job.SourceClusterID)
	if err != nil {
		o.failJob(ctx, jobID, fmt.Sprintf("source cluster client: %v", err))
		return
	}

	var upid string

	switch job.MigrationType {
	case TypeIntraCluster:
		upid, err = o.executeIntraCluster(ctx, srcClient, job)
	case TypeCrossCluster:
		upid, err = o.executeCrossCluster(ctx, srcClient, srcCluster, job)
	default:
		o.failJob(ctx, jobID, fmt.Sprintf("unknown migration type: %s", job.MigrationType))
		return
	}

	if err != nil {
		o.failJob(ctx, jobID, err.Error())
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

	// Poll task status.
	o.pollTaskStatus(ctx, srcClient, job.SourceNode, upid, jobID)
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

func (o *Orchestrator) pollTaskStatus(ctx context.Context, client *proxmox.Client, node, upid string, jobID uuid.UUID) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			o.failJob(ctx, jobID, "context cancelled")
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

			// Task completed.
			now := pgtype.Timestamptz{Time: time.Now(), Valid: true}

			if status.ExitStatus == "OK" {
				_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
					ID:          jobID,
					Status:      StatusCompleted,
					CompletedAt: now,
				})
				o.logger.Info("migration completed", "job_id", jobID)
			} else {
				_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
					ID:           jobID,
					Status:       StatusFailed,
					CompletedAt:  now,
					ErrorMessage: fmt.Sprintf("Task exit status: %s", status.ExitStatus),
				})
				o.logger.Error("migration failed", "job_id", jobID, "exit_status", status.ExitStatus)
			}
			return
		}
	}
}

func (o *Orchestrator) failJob(ctx context.Context, jobID uuid.UUID, errMsg string) {
	o.logger.Error("migration job failed", "job_id", jobID, "error", errMsg)
	_ = o.queries.CompleteMigrationJob(ctx, db.CompleteMigrationJobParams{
		ID:           jobID,
		Status:       StatusFailed,
		CompletedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		ErrorMessage: errMsg,
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
