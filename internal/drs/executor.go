package drs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/redact"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// executorQueries is the database surface Execute uses, behind an interface so
// its failure paths can be tested without a database, as with evalQueries.
type executorQueries interface {
	InsertDRSHistory(ctx context.Context, arg db.InsertDRSHistoryParams) (db.DrsHistory, error)
	UpdateDRSHistoryStatus(ctx context.Context, arg db.UpdateDRSHistoryStatusParams) error
	InsertTaskHistory(ctx context.Context, arg db.InsertTaskHistoryParams) (db.TaskHistory, error)
	ReconcileTaskHistory(ctx context.Context, arg db.ReconcileTaskHistoryParams) (int64, error)
	GetVMByClusterAndVmid(ctx context.Context, arg db.GetVMByClusterAndVmidParams) (db.Vm, error)
	InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) error
}

// Executor handles DRS migration execution and history recording.
type Executor struct {
	queries  executorQueries
	logger   *slog.Logger
	eventPub *events.Publisher
	// shutdownCtx is the parent for any context that must outlive a
	// scheduler tick but should still be cancelled on graceful shutdown
	// (SIGTERM). nil falls back to context.Background() so a partially
	// constructed Executor (tests) still works.
	shutdownCtx context.Context
	// taskPollInterval is how often Execute asks Proxmox about a migration.
	taskPollInterval time.Duration
	// haStartGrace is how long followHAMigration waits for a migration to start.
	haStartGrace time.Duration
}

// NewExecutor creates a new DRS executor. shutdownCtx should be the
// per-server context that's cancelled on SIGTERM; pass context.Background()
// from contexts that don't have one (e.g. tests).
func NewExecutor(shutdownCtx context.Context, queries *db.Queries, logger *slog.Logger, eventPub *events.Publisher) *Executor {
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}
	return &Executor{
		queries:          queries,
		logger:           logger,
		eventPub:         eventPub,
		shutdownCtx:      shutdownCtx,
		taskPollInterval: 5 * time.Second,
		haStartGrace:     2 * time.Minute,
	}
}

// Execute processes recommendations based on the DRS mode.
// In "advisory" mode, only records to history. In "automatic" mode, executes migrations.
func (e *Executor) Execute(ctx context.Context, client *proxmox.Client, clusterID uuid.UUID, mode string, recommendations []Recommendation) error {
	for _, rec := range recommendations {
		history, err := e.queries.InsertDRSHistory(ctx, db.InsertDRSHistoryParams{
			ClusterID:   clusterID,
			SourceNode:  rec.SourceNode,
			TargetNode:  rec.TargetNode,
			VmID:        safeconv.Int32(rec.VMID),
			VmType:      rec.VMType,
			Reason:      rec.Reason,
			ScoreBefore: rec.ScoreBefore,
			ScoreAfter:  rec.ScoreAfter,
			Status:      "pending",
			ExecutedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		if err != nil {
			e.logger.Error("failed to record DRS history", "error", err)
			continue
		}

		// Resolve VM database ID for audit log linking.
		vmDBID := e.resolveVMDBID(ctx, clusterID, rec.VMID)

		if mode == "advisory" {
			e.logger.Info("DRS advisory recommendation",
				"vmid", rec.VMID, "type", rec.VMType,
				"from", rec.SourceNode, "to", rec.TargetNode,
				"reason", rec.Reason)
			_ = e.queries.UpdateDRSHistoryStatus(ctx, db.UpdateDRSHistoryStatusParams{
				ID:         history.ID,
				Status:     "advisory",
				ExecutedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
			e.auditLog(ctx, clusterID, vmDBID, "drs_advisory", rec, nil)
			e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "advisory")
			continue
		}

		// Automatic mode: execute the migration.
		e.logger.Info("DRS executing migration",
			"vmid", rec.VMID, "type", rec.VMType,
			"from", rec.SourceNode, "to", rec.TargetNode)

		params := proxmox.MigrateParams{
			Target: rec.TargetNode,
			Online: true,
		}

		var upid string
		var migrateErr error

		switch rec.VMType {
		case "qemu":
			upid, migrateErr = client.MigrateVM(ctx, rec.SourceNode, rec.VMID, params)
		case "lxc":
			upid, migrateErr = client.MigrateCT(ctx, rec.SourceNode, rec.VMID, params)
		default:
			migrateErr = fmt.Errorf("unsupported VM type: %s", rec.VMType)
		}

		if migrateErr != nil {
			e.logger.Error("DRS migration failed",
				"vmid", rec.VMID, "error", migrateErr)
			_ = e.queries.UpdateDRSHistoryStatus(ctx, db.UpdateDRSHistoryStatusParams{
				ID:         history.ID,
				Status:     "failed",
				ExecutedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
			e.auditLog(ctx, clusterID, vmDBID, "drs_migrate_failed", rec, failureDetails(migrateErr.Error()))
			e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "migrate_failed")
			continue
		}

		// Insert into task_history so the task panel shows this migration.
		description := fmt.Sprintf("DRS Migrate %s %d: %s → %s",
			rec.VMType, rec.VMID, rec.SourceNode, rec.TargetNode)
		_, taskErr := e.queries.InsertTaskHistory(ctx, db.InsertTaskHistoryParams{
			ClusterID:   clusterID,
			UserID:      auth.SystemUserID,
			Upid:        upid,
			Description: description,
			Status:      "running",
			Node:        rec.SourceNode,
			TaskType:    "drs_migrate",
		})
		if taskErr != nil {
			e.logger.Warn("failed to insert task history for DRS migration",
				"upid", upid, "error", taskErr)
		}
		e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindTaskCreated, "task", upid, "drs_migrate")
		e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "migrate_started")

		// Audit log with UPID so the activity pane can show a progress bar.
		e.auditLog(ctx, clusterID, vmDBID, "drs_migrate_running", rec, map[string]any{"upid": upid, "node": rec.SourceNode})

		// Poll task status until completion.
		status, exitStatus := e.waitForTask(ctx, client, rec.SourceNode, upid)
		taskStatus := status

		// Finalize the task_history row. ReconcileTaskHistory is guarded on
		// status='running', so it won't clobber a row the collector reconciler
		// already finalized; status is "completed"/"failed" from waitForTask.
		//
		// exitStatus is PVE's text verbatim, UNSCRUBBED. Safe only because DRS
		// issues MigrateVM/MigrateCT and never RemoteMigrate, so no target
		// cluster's API token is ever handed to Proxmox on this path and its
		// die message cannot echo one back. Note DRS also creates no
		// migration_jobs rows, so neither credential guard that protects
		// cross-cluster migration (the anti-join in
		// ListRunningTaskHistoryByCluster, the lookup in seenTaskUPIDs) would
		// cover this write — giving DRS a remote target needs both, plus a
		// scrub here.
		_, _ = e.queries.ReconcileTaskHistory(ctx, db.ReconcileTaskHistoryParams{
			Upid:       upid,
			Status:     status,
			ExitStatus: exitStatus,
			FinishedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})

		// The history and the audit log record the migration, not the task that
		// asked for it, so an HA request is followed to the task that does it.
		if since, isHA := haRequestStart(upid); isHA && status == "completed" {
			status, exitStatus = e.followHAMigration(client, rec, since)
		}
		_ = e.queries.UpdateDRSHistoryStatus(ctx, db.UpdateDRSHistoryStatusParams{
			ID:         history.ID,
			Status:     status,
			ExecutedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})

		// Audit log the result.
		if status == "completed" {
			e.auditLog(ctx, clusterID, vmDBID, "drs_migrate", rec, nil)
			e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "migrate_completed")
			e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindInventoryChange, "vm", vmDBID, "drs_migrated")
		} else {
			e.auditLog(ctx, clusterID, vmDBID, "drs_migrate_failed", rec, failureDetails(exitStatus))
			e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "migrate_failed")
		}
		e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindTaskUpdate, "task", upid, taskStatus)

		e.logger.Info("DRS migration completed",
			"vmid", rec.VMID, "status", status, "upid", upid)
	}

	return nil
}

// haRequestStart returns the start time of a task that only asks the HA manager
// for a migration, and whether upid is one. For an HA-managed guest Proxmox answers
// the migrate call with an hamigrate task, which queues the request and ends OK
// within a second, whatever becomes of the migration.
func haRequestStart(upid string) (int64, bool) {
	parts := strings.Split(upid, ":")
	if len(parts) < 7 || parts[5] != "hamigrate" {
		return 0, false
	}
	start, err := strconv.ParseInt(parts[4], 16, 64)
	return start, err == nil
}

// followHAMigration waits for the migration the HA manager starts after an
// hamigrate task and returns how it ended. Taking the hamigrate's OK instead
// records a migration that fails as completed, so the guest is retried on the next
// tick with nothing in the audit log to say why.
//
// The HA manager runs it on the guest's node, usually within 20 s, though it shuts
// a container down first, for up to a minute. If none starts within the grace the
// outcome is unknown, reported as "timeout" like a task DRS stops waiting for. The
// task list is read from since, the hamigrate's start, so an earlier attempt's task
// cannot be taken for this one's.
func (e *Executor) followHAMigration(client *proxmox.Client, rec Recommendation, since int64) (status string, detail string) {
	ctx, cancel := context.WithTimeout(e.shutdownCtx, e.haStartGrace)
	defer cancel()

	ticker := time.NewTicker(e.taskPollInterval)
	defer ticker.Stop()

	vmid := strconv.Itoa(rec.VMID)
	for {
		select {
		case <-ctx.Done():
			return "timeout", fmt.Sprintf("the HA manager started no migration within %s", e.haStartGrace)
		case <-ticker.C:
			tasks, err := client.GetNodeTasks(ctx, rec.SourceNode, since, 0)
			if err != nil {
				e.logger.Warn("failed to list tasks for an HA migration", "vmid", rec.VMID, "error", err)
				continue
			}
			for _, t := range tasks {
				if t.ID == vmid && (t.Type == "qmigrate" || t.Type == "vzmigrate") {
					return e.waitForTask(ctx, client, rec.SourceNode, t.UPID)
				}
			}
		}
	}
}

func (e *Executor) waitForTask(_ context.Context, client *proxmox.Client, node string, upid string) (status string, detail string) {
	// Use a dedicated timeout context derived from the per-server shutdown
	// context. Live migrations can take 15+ minutes for large VMs, so we
	// don't tie this to the scheduler tick context. We DO honor SIGTERM via
	// shutdownCtx so a `docker compose stop` cancels the poll loop instead
	// of orphaning it.
	pollCtx, cancel := context.WithTimeout(e.shutdownCtx, 30*time.Minute)
	defer cancel()

	ticker := time.NewTicker(e.taskPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pollCtx.Done():
			// Do one final check — the migration may have finished while we
			// were waiting. Use a short independent context (NOT derived from
			// shutdownCtx) so we still record the outcome during graceful
			// shutdown rather than orphan a tracked task.
			finalCtx, finalCancel := context.WithTimeout(context.Background(), 10*time.Second)
			ts, err := client.GetTaskStatus(finalCtx, node, upid)
			finalCancel()
			if err == nil && ts.Status == "stopped" {
				if proxmox.TaskSucceeded(ts.ExitStatus) {
					return "completed", ts.ExitStatus
				}
				return "failed", ts.ExitStatus
			}
			return "timeout", "task timed out after 30 minutes"
		case <-ticker.C:
			ts, err := client.GetTaskStatus(pollCtx, node, upid)
			if err != nil {
				e.logger.Warn("failed to poll task status", "upid", upid, "error", err)
				continue
			}
			if ts.Status == "stopped" {
				if proxmox.TaskSucceeded(ts.ExitStatus) {
					return "completed", ts.ExitStatus
				}
				return "failed", ts.ExitStatus
			}
		}
	}
}

// resolveVMDBID looks up the VM's database UUID for audit log linking.
func (e *Executor) resolveVMDBID(ctx context.Context, clusterID uuid.UUID, vmid int) string {
	vm, err := e.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
		ClusterID: clusterID,
		Vmid:      safeconv.Int32(vmid),
	})
	if err != nil {
		return ""
	}
	return vm.ID.String()
}

// failureDetails is what a drs_migrate_failed row adds: Proxmox's own text. Audit
// details are readable by every Viewer, so a URL in it keeps no userinfo, query or
// fragment, as when GetTaskStatus serves the same text.
func failureDetails(text string) map[string]any {
	return map[string]any{"error": redact.URLs(text)}
}

// auditLog inserts an audit log entry for a DRS action, with extra added to the
// details every entry carries.
func (e *Executor) auditLog(ctx context.Context, clusterID uuid.UUID, resourceID, action string, rec Recommendation, extra map[string]any) {
	fields := map[string]any{
		"vmid":        rec.VMID,
		"vm_type":     rec.VMType,
		"source_node": rec.SourceNode,
		"target_node": rec.TargetNode,
		"reason":      rec.Reason,
		"improvement": fmt.Sprintf("%.1f%%", rec.ExpectedImprovement*100),
	}
	maps.Copy(fields, extra)
	details, _ := json.Marshal(fields)

	if resourceID == "" {
		resourceID = fmt.Sprintf("vmid:%d", rec.VMID)
	}

	err := e.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: clusterID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: "vm",
		ResourceID:   resourceID,
		Action:       action,
		Details:      details,
	})
	if err != nil {
		e.logger.Warn("failed to insert DRS audit log", "action", action, "error", err)
	}
}
