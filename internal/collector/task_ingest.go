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

		for _, task := range tasks {
			if task.StartTime > maxStartTime {
				maxStartTime = task.StartTime
			}

			if err := s.ingestTask(ctx, cluster, node.Node, task); err != nil {
				s.logger.Warn("task sync: failed to ingest task",
					"cluster_id", cluster.ID,
					"upid", task.UPID,
					"error", err,
				)
			}
		}
	}

	// Update high-water mark if we processed any tasks.
	if maxStartTime > since {
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

// ingestTask records a single external (non-Nexara) Proxmox task: a task_history
// row (status from the task — running or classified terminal — so the reconciler
// and the Tasks page see it) plus an audit_log entry (so the activity feed shows
// it), attributed to the system user. Nexara-initiated tasks and tasks already
// seen on a prior tick are skipped.
func (s *Syncer) ingestTask(ctx context.Context, cluster db.Cluster, nodeName string, task proxmox.NodeTask) error {
	// Skip noisy task types.
	if skipTaskTypes[task.Type] {
		return nil
	}

	// Dedup layer 1: skip if UPID exists in task_history. Catches Nexara-
	// dispatched tasks (already tracked at dispatch) AND external tasks ingested
	// on a prior tick — once a task_history row exists, the collector reconciler
	// owns its running→done transition, so re-ingesting would be redundant.
	exists, err := s.queries.ExistsTaskHistoryByUPID(ctx, task.UPID)
	if err != nil {
		return fmt.Errorf("check task_history UPID: %w", err)
	}
	if exists {
		return nil
	}

	// Dedup layer 2: skip if UPID was already ingested into audit_log.
	alreadyIngested, err := s.queries.ExistsAuditLogByUPID(ctx, task.UPID)
	if err != nil {
		return fmt.Errorf("check audit_log UPID: %w", err)
	}
	if alreadyIngested {
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
