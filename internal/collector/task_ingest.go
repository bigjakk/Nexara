package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// syncTasks fetches completed tasks from all nodes in a cluster and ingests
// external (non-Nexara) tasks into the audit log.
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

// ingestTask processes a single Proxmox task and inserts it into the audit log
// if it wasn't initiated by Nexara.
func (s *Syncer) ingestTask(ctx context.Context, cluster db.Cluster, nodeName string, task proxmox.NodeTask) error {
	// Skip noisy task types.
	if skipTaskTypes[task.Type] {
		return nil
	}

	// Skip tasks that are still running (Status is empty string).
	if task.Status == "" {
		return nil
	}

	// Dedup layer 1: skip if UPID exists in task_history (Nexara-initiated).
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
		UserID:       systemUserID,
		ResourceType: mapping.ResourceType,
		ResourceID:   resourceID,
		Action:       mapping.Action,
		Details:      details,
		Source:       "proxmox",
		CreatedAt:    time.Unix(task.StartTime, 0),
	}); err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}

	// Publish event for real-time WS updates.
	if s.eventPub != nil {
		s.eventPub.ClusterEvent(ctx, cluster.ID.String(),
			events.KindAuditEntry, mapping.ResourceType, resourceID, mapping.Action)
	}

	return nil
}

