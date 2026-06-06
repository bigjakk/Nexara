package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// syncTasks fetches active + finished tasks from every node in a cluster and
// records external (non-Nexara) tasks. Running→done transitions are owned by
// the separate reconcileRunningTasks pass, not by re-ingestion here.
func (s *Syncer) syncTasks(ctx context.Context, client ProxmoxClient, cluster db.Cluster) {
	// Load high-water mark (default: 24h ago on first run).
	since, err := s.queries.GetTaskSyncState(ctx, cluster.ID)
	if err != nil {
		since = time.Now().Add(-24 * time.Hour).Unix()
	}

	nodes, err := client.GetNodes(ctx)
	if err != nil {
		s.logger.Warn("task sync: failed to get nodes",
			"cluster_id", cluster.ID,
			"error", err,
		)
		return
	}

	var maxStartTime int64
	dedupFailed := false
	for _, node := range nodes {
		tasks, err := client.GetNodeTasks(ctx, node.Node, since, 500)
		if err != nil {
			s.logger.Warn("task sync: failed to get tasks",
				"cluster_id", cluster.ID,
				"node", node.Node,
				"error", err,
			)
			continue
		}

		// Resolve which UPIDs are already recorded in one batch per node (two
		// indexed lookups) instead of a per-task SELECT pair.
		candidates := make([]string, 0, len(tasks))
		for _, task := range tasks {
			if !skipTaskTypes[task.Type] {
				candidates = append(candidates, task.UPID)
			}
		}
		seen, dedupErr := s.seenTaskUPIDs(ctx, cluster, candidates)
		if dedupErr != nil {
			// Can't dedup this node's tasks safely this tick. Skip ingestion and
			// hold the cluster watermark back (below) so these tasks are retried
			// next tick instead of being skipped past forever by the since-filter.
			// A later successful tick re-lists them and seenTaskUPIDs skips any we
			// already recorded (InsertExternalTaskHistory is ON CONFLICT DO NOTHING).
			dedupFailed = true
			continue
		}

		for _, task := range tasks {
			if task.StartTime > maxStartTime {
				maxStartTime = task.StartTime
			}
			if err := s.ingestTask(ctx, cluster, node.Node, task, seen); err != nil {
				s.logger.Warn("task sync: failed to ingest task",
					"cluster_id", cluster.ID,
					"upid", task.UPID,
					"error", err,
				)
			}
		}
	}

	// Advance the high-water mark only when every node deduped cleanly. If any
	// node's dedup failed we processed a partial set, so leaving `since` where it
	// is forces a full retry next tick rather than silently skipping the tasks we
	// could not ingest.
	if !dedupFailed && maxStartTime > since {
		if err := s.queries.UpsertTaskSyncState(ctx, db.UpsertTaskSyncStateParams{
			ClusterID:    cluster.ID,
			LastSyncedAt: maxStartTime,
		}); err != nil {
			s.logger.Warn("task sync: failed to update sync state",
				"cluster_id", cluster.ID,
				"error", err,
			)
		}
	}
}

// seenTaskUPIDs returns the subset of the candidate UPIDs already recorded — in
// task_history (Nexara-dispatched or a prior external ingest) or audit_log (any
// source) — so ingestTask can skip them. Two indexed batch lookups replace the
// old per-task 2×N point SELECTs. Returns an error if either lookup fails so the
// caller can skip ingestion rather than risk duplicate rows from a partial set.
func (s *Syncer) seenTaskUPIDs(ctx context.Context, cluster db.Cluster, upids []string) (map[string]bool, error) {
	seen := make(map[string]bool, len(upids))
	if len(upids) == 0 {
		return seen, nil
	}
	thUPIDs, err := s.queries.ListExistingTaskHistoryUPIDs(ctx, upids)
	if err != nil {
		s.logger.Warn("task sync: batch task_history UPID check failed",
			"cluster_id", cluster.ID, "error", err)
		return seen, err
	}
	for _, u := range thUPIDs {
		seen[u] = true
	}
	alUPIDs, err := s.queries.ListExistingAuditLogUPIDs(ctx, upids)
	if err != nil {
		s.logger.Warn("task sync: batch audit_log UPID check failed",
			"cluster_id", cluster.ID, "error", err)
		return seen, err
	}
	for _, u := range alUPIDs {
		seen[u] = true
	}
	return seen, nil
}

// ingestTask records a single external (non-Nexara) Proxmox task: a task_history
// row (status from the task — running or classified terminal — so the reconciler
// and the Tasks page see it) plus an audit_log entry (so the activity feed shows
// it), attributed to the system user. Nexara-initiated tasks and tasks already
// seen on a prior tick are skipped.
func (s *Syncer) ingestTask(ctx context.Context, cluster db.Cluster, nodeName string, task proxmox.NodeTask, seen map[string]bool) error {
	// Skip noisy task types.
	if skipTaskTypes[task.Type] {
		return nil
	}

	// Dedup: skip if already recorded (task_history — Nexara-dispatched or a
	// prior external ingest — or audit_log, any source). The seen set is
	// resolved once per node in syncTasks via two batch lookups; once a
	// task_history row exists, the collector reconciler owns its running→done
	// transition, so re-ingesting would be redundant.
	if seen[task.UPID] {
		return nil
	}

	// Map task type to audit log resource_type + action.
	mapping, ok := proxmoxTaskMap[task.Type]
	if !ok {
		// Unmapped types fall through as generic proxmox_task.
		mapping = taskMapping{
			ResourceType: "proxmox_task",
			Action:       task.Type,
		}
	}

	// Resolve VM/CT name from the database when the task targets a guest.
	resourceName := ""
	if task.ID != "" && (mapping.ResourceType == "vm" || mapping.ResourceType == "container" || mapping.ResourceType == "backup") {
		if vmid, err := strconv.ParseInt(task.ID, 10, 32); err == nil {
			if vm, err := s.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
				ClusterID: cluster.ID,
				Vmid:      int32(vmid),
			}); err == nil {
				resourceName = vm.Name
			}
		}
	}

	// Record the task in task_history so the collector reconciler tracks its
	// running→done lifecycle and the activity feed + Tasks page show live,
	// server-authoritative status — exactly like a Nexara-dispatched task.
	// Running tasks (Status == "") are stored as "running"; tasks already
	// finished when first seen are stored fully-formed.
	taskStatus := "running"
	var taskExit string
	var finishedAt pgtype.Timestamptz
	if task.Status != "" {
		taskStatus, taskExit = classifyTaskExit(task.Status)
		endTime := task.EndTime
		if endTime == 0 {
			endTime = task.StartTime
		}
		finishedAt = pgtype.Timestamptz{Time: time.Unix(endTime, 0), Valid: true}
	}
	taskDescription := mapping.Action
	if resourceName != "" {
		taskDescription += " " + resourceName
	} else if task.ID != "" {
		taskDescription += " " + task.ID
	}
	if err := s.queries.InsertExternalTaskHistory(ctx, db.InsertExternalTaskHistoryParams{
		ClusterID:   cluster.ID,
		UserID:      auth.SystemUserID,
		Upid:        task.UPID,
		Description: taskDescription,
		Status:      taskStatus,
		ExitStatus:  taskExit,
		Node:        nodeName,
		TaskType:    task.Type,
		StartedAt:   time.Unix(task.StartTime, 0),
		FinishedAt:  finishedAt,
	}); err != nil {
		return fmt.Errorf("insert external task_history: %w", err)
	}

	// Build details JSON.
	detailMap := map[string]interface{}{
		"upid":         task.UPID,
		"proxmox_user": task.User,
		"node":         nodeName,
		"status":       task.Status,
		"task_type":    task.Type,
		"resource_id":  task.ID,
	}
	if resourceName != "" {
		detailMap["resource_name"] = resourceName
	}
	details, _ := json.Marshal(detailMap)

	// Determine resource ID. For VM/CT tasks, use the VMID from the task.
	resourceID := task.ID
	if resourceID == "" {
		resourceID = nodeName
	}

	if err := s.queries.InsertAuditLogWithSource(ctx, db.InsertAuditLogWithSourceParams{
		ClusterID:    pgtype.UUID{Bytes: cluster.ID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: mapping.ResourceType,
		ResourceID:   resourceID,
		Action:       mapping.Action,
		Details:      details,
		Source:       "proxmox",
		CreatedAt:    time.Unix(task.StartTime, 0),
	}); err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}

	// Publish events for real-time WS updates: KindTaskCreated drives the Tasks
	// page + activity feed; KindAuditEntry drives the audit-log page + feed. The
	// reconciler later fires KindTaskUpdate when a running task finishes.
	if s.eventPub != nil {
		s.eventPub.ClusterEvent(ctx, cluster.ID.String(),
			events.KindTaskCreated, "task", task.UPID, task.Type)
		s.eventPub.ClusterEvent(ctx, cluster.ID.String(),
			events.KindAuditEntry, mapping.ResourceType, resourceID, mapping.Action)
	}

	return nil
}
