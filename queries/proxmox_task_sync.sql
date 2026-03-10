-- name: GetTaskSyncState :one
SELECT last_synced_at FROM proxmox_task_sync_state WHERE cluster_id = $1;

-- name: UpsertTaskSyncState :exec
INSERT INTO proxmox_task_sync_state (cluster_id, last_synced_at)
VALUES ($1, $2)
ON CONFLICT (cluster_id) DO UPDATE SET last_synced_at = $2;

-- name: ExistsTaskHistoryByUPID :one
SELECT EXISTS(SELECT 1 FROM task_history WHERE upid = $1) AS exists;

-- name: ExistsAuditLogByUPID :one
SELECT EXISTS(SELECT 1 FROM audit_log WHERE source = 'proxmox' AND details->>'upid' = sqlc.arg('upid')::text) AS exists;
