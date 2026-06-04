-- name: GetTaskSyncState :one
SELECT last_synced_at FROM proxmox_task_sync_state WHERE cluster_id = $1;

-- name: UpsertTaskSyncState :exec
INSERT INTO proxmox_task_sync_state (cluster_id, last_synced_at)
VALUES ($1, $2)
ON CONFLICT (cluster_id) DO UPDATE SET last_synced_at = $2;

-- ListExistingTaskHistoryUPIDs and ListExistingAuditLogUPIDs are the batch
-- dedup the collector ingest uses: given a node's candidate UPIDs, return the
-- subset already recorded, so ingestTask skips them without a per-task SELECT
-- (the security review flagged the old 2×N point lookups). Together they cover
-- the original two dedup layers — task_history (Nexara-dispatched or
-- already-ingested external) and audit_log (any source: a UI action Nexara
-- already audited, or a legacy external task ingested before task_history rows
-- existed). Backed by idx_task_history_upid and idx_audit_log_upid.

-- name: ListExistingTaskHistoryUPIDs :many
SELECT upid FROM task_history WHERE upid = ANY(sqlc.arg('upids')::text[]);

-- name: ListExistingAuditLogUPIDs :many
SELECT (details->>'upid')::text AS upid FROM audit_log WHERE details->>'upid' = ANY(sqlc.arg('upids')::text[]);
