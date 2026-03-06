package drs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// Executor handles DRS migration execution and history recording.
type Executor struct {
	queries *db.Queries
	logger  *slog.Logger
}

// NewExecutor creates a new DRS executor.
func NewExecutor(queries *db.Queries, logger *slog.Logger) *Executor {
	return &Executor{
		queries: queries,
		logger:  logger,
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
		})
		if err != nil {
			e.logger.Error("failed to record DRS history", "error", err)
			continue
		}

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
			continue
		}

		// Poll task status until completion.
		status := e.waitForTask(ctx, client, rec.SourceNode, upid)
		_ = e.queries.UpdateDRSHistoryStatus(ctx, db.UpdateDRSHistoryStatusParams{
			ID:         history.ID,
			Status:     status,
			ExecutedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})

		e.logger.Info("DRS migration completed",
			"vmid", rec.VMID, "status", status, "upid", upid)
	}

	return nil
}

func (e *Executor) waitForTask(ctx context.Context, client *proxmox.Client, node string, upid string) string {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	timeout := time.After(10 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return "cancelled"
		case <-timeout:
			return "timeout"
		case <-ticker.C:
			ts, err := client.GetTaskStatus(ctx, node, upid)
			if err != nil {
				e.logger.Warn("failed to poll task status", "upid", upid, "error", err)
				continue
			}
			if ts.Status == "stopped" {
				if ts.ExitStatus == "OK" {
					return "completed"
				}
				return "failed"
			}
		}
	}
}
