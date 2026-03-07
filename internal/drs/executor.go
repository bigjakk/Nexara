package drs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// SystemUserID is the well-known UUID for the DRS scheduler system user.
// Created by migration 000013_system_user.
var SystemUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// Executor handles DRS migration execution and history recording.
type Executor struct {
	queries  *db.Queries
	logger   *slog.Logger
	eventPub *events.Publisher
}

// NewExecutor creates a new DRS executor.
func NewExecutor(queries *db.Queries, logger *slog.Logger, eventPub *events.Publisher) *Executor {
	return &Executor{
		queries:  queries,
		logger:   logger,
		eventPub: eventPub,
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
			VmID:        int32(rec.VMID),
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
			e.auditLog(ctx, clusterID, vmDBID, "drs_advisory", rec)
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
			e.auditLog(ctx, clusterID, vmDBID, "drs_migrate_failed", rec)
			e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "migrate_failed")
			continue
		}

		// Insert into task_history so the task panel shows this migration.
		description := fmt.Sprintf("DRS Migrate %s %d: %s → %s",
			rec.VMType, rec.VMID, rec.SourceNode, rec.TargetNode)
		_, taskErr := e.queries.InsertTaskHistory(ctx, db.InsertTaskHistoryParams{
			ClusterID:   clusterID,
			UserID:      SystemUserID,
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

		// Poll task status until completion.
		status, exitStatus := e.waitForTask(ctx, client, rec.SourceNode, upid)
		_ = e.queries.UpdateDRSHistoryStatus(ctx, db.UpdateDRSHistoryStatusParams{
			ID:         history.ID,
			Status:     status,
			ExecutedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})

		// Update task_history with final status.
		_ = e.queries.UpdateTaskHistory(ctx, db.UpdateTaskHistoryParams{
			Upid:       upid,
			Status:     "stopped",
			ExitStatus: exitStatus,
			FinishedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})

		// Audit log the result.
		if status == "completed" {
			e.auditLog(ctx, clusterID, vmDBID, "drs_migrate", rec)
			e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "migrate_completed")
			e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindInventoryChange, "vm", vmDBID, "drs_migrated")
		} else {
			e.auditLog(ctx, clusterID, vmDBID, "drs_migrate_failed", rec)
			e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "migrate_failed")
		}
		e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindTaskUpdate, "task", upid, status)

		e.logger.Info("DRS migration completed",
			"vmid", rec.VMID, "status", status, "upid", upid)
	}

	return nil
}

// taskSucceeded returns true if a Proxmox task exit status indicates success.
// Proxmox uses "OK" for clean exits but can also return statuses like
// "WARNINGS" or "OK (with warnings)" which still mean the task completed.
// An empty exit status with a stopped task also indicates success on some
// Proxmox versions.
func taskSucceeded(exitStatus string) bool {
	upper := strings.ToUpper(strings.TrimSpace(exitStatus))
	return upper == "" || upper == "OK" || strings.HasPrefix(upper, "OK ") || upper == "WARNINGS"
}

func (e *Executor) waitForTask(ctx context.Context, client *proxmox.Client, node string, upid string) (string, string) {
	// Use a dedicated timeout context so that scheduler shutdown doesn't
	// prematurely mark in-flight migrations as cancelled. Live migrations
	// can take 15+ minutes for large VMs.
	pollCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-pollCtx.Done():
			// Do one final check — the migration may have finished while we
			// were waiting. Use a short independent context for the API call.
			finalCtx, finalCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer finalCancel()
			ts, err := client.GetTaskStatus(finalCtx, node, upid)
			if err == nil && ts.Status == "stopped" {
				if taskSucceeded(ts.ExitStatus) {
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
				if taskSucceeded(ts.ExitStatus) {
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
		Vmid:      int32(vmid),
	})
	if err != nil {
		return ""
	}
	return vm.ID.String()
}

// auditLog inserts an audit log entry for a DRS action.
func (e *Executor) auditLog(ctx context.Context, clusterID uuid.UUID, resourceID, action string, rec Recommendation) {
	details, _ := json.Marshal(map[string]interface{}{
		"vmid":        rec.VMID,
		"vm_type":     rec.VMType,
		"source_node": rec.SourceNode,
		"target_node": rec.TargetNode,
		"reason":      rec.Reason,
		"improvement": fmt.Sprintf("%.1f%%", rec.ExpectedImprovement*100),
	})

	if resourceID == "" {
		resourceID = fmt.Sprintf("vmid:%d", rec.VMID)
	}

	err := e.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: clusterID, Valid: true},
		UserID:       SystemUserID,
		ResourceType: "vm",
		ResourceID:   resourceID,
		Action:       action,
		Details:      details,
	})
	if err != nil {
		e.logger.Warn("failed to insert DRS audit log", "action", action, "error", err)
	}
}
