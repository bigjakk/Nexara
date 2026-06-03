package collector

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// staleTaskGrace bounds how long a task_history row may stay "running" once
// Proxmox can no longer report its status (node reboot, task-log rotation)
// before we give up and mark it failed.
const staleTaskGrace = 24 * time.Hour

// reconcileRunningTasks updates task_history rows still marked "running" by
// polling each task's live status from Proxmox. The working set is tiny (only
// in-flight Nexara-dispatched tasks), so one GetTaskStatus call per row per
// sync tick is cheap. Finished tasks are flipped to completed/failed and a
// task_update event is published so the activity feed refreshes.
//
// This is the source of truth that lets the UI stop polling Proxmox per entry.
func (s *Syncer) reconcileRunningTasks(ctx context.Context, client ProxmoxClient, cluster db.Cluster) {
	rows, err := s.queries.ListRunningTaskHistoryByCluster(ctx, cluster.ID)
	if err != nil {
		s.logger.Warn("task reconcile: failed to list running tasks",
			"cluster_id", cluster.ID,
			"error", err,
		)
		return
	}

	for _, row := range rows {
		st, err := client.GetTaskStatus(ctx, row.Node, row.Upid)
		if err != nil {
			// Task may have aged out of Proxmox's task log. If it has been
			// "running" well past any plausible lifetime, mark it failed so it
			// doesn't hang forever; otherwise leave it and retry next tick.
			if time.Since(row.StartedAt) > staleTaskGrace {
				s.finalizeTask(ctx, cluster, row.Upid, "failed", "vanished")
			}
			continue
		}
		if st.Status != "stopped" {
			continue // still running
		}
		status, exit := classifyTaskExit(st.ExitStatus)
		s.finalizeTask(ctx, cluster, row.Upid, status, exit)
	}
}

// finalizeTask flips a still-running task_history row to a terminal state and,
// when a row actually changed, publishes a task_update event. The underlying
// query is scoped to status='running', so it never clobbers rows already
// finalized by the migration orchestrator or DRS executor.
func (s *Syncer) finalizeTask(ctx context.Context, cluster db.Cluster, upid, status, exitStatus string) {
	n, err := s.queries.ReconcileTaskHistory(ctx, db.ReconcileTaskHistoryParams{
		Upid:       upid,
		Status:     status,
		ExitStatus: exitStatus,
		FinishedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		s.logger.Warn("task reconcile: failed to update task",
			"cluster_id", cluster.ID,
			"upid", upid,
			"error", err,
		)
		return
	}
	if n > 0 && s.eventPub != nil {
		s.eventPub.ClusterEvent(ctx, cluster.ID.String(), events.KindTaskUpdate, "task", upid, status)
	}
}

// classifyTaskExit maps a Proxmox task exitstatus to (status, exit_status).
// "", "OK" and "WARNINGS: …" count as success; anything else is a failure.
// Mirrors the success rule used by the task-detail UI.
func classifyTaskExit(exitStatus string) (status, exit string) {
	if exitStatus == "" || exitStatus == "OK" || strings.HasPrefix(exitStatus, "WARNINGS") {
		return "completed", exitStatus
	}
	return "failed", exitStatus
}
