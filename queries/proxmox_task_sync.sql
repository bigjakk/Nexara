-- name: GetTaskSyncState :one
SELECT last_synced_at FROM proxmox_task_sync_state WHERE cluster_id = $1;

-- name: UpsertTaskSyncState :exec
INSERT INTO proxmox_task_sync_state (cluster_id, last_synced_at)
VALUES ($1, $2)
ON CONFLICT (cluster_id) DO UPDATE SET last_synced_at = $2;

-- name: ExistsTaskHistoryByUPID :one
SELECT EXISTS(SELECT 1 FROM task_history WHERE upid = $1) AS exists;

-- name: ExistsAuditLogByUPID :one
-- Cross-source UPID lookup: returns true if ANY audit_log row references
-- this UPID, regardless of whether it was written by the Nexara handler
-- (source='nexara') or previously ingested from Proxmox
-- (source='proxmox'). Used by collector/task_ingest.go to skip
-- ingesting tasks Nexara already audited — without that, the user sees
-- duplicate activity rows when they trigger an action through the UI
-- (one from the handler, one from the post-hoc proxmox task ingest).
-- Backed by idx_audit_log_upid (migration 000060).
SELECT EXISTS(SELECT 1 FROM audit_log WHERE details->>'upid' = sqlc.arg('upid')::text) AS exists;
