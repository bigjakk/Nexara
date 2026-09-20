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

-- ListCrossClusterMigrationUPIDs is the third dedup layer, and unlike its two
-- siblings it is a CREDENTIAL boundary rather than a duplicate-row guard.
--
-- A cross-cluster migration hands Proxmox the target cluster's decrypted API
-- token in the `target-endpoint` property string, so the worker's die message
-- can echo it back. ingestTask writes PVE's status straight into
-- task_history.exit_status at INSERT — readable at view:task and, via the task
-- join, at view:audit — and 'qmigrate' is not in skipTaskTypes, so nothing else
-- keeps it out. Only internal/migration holds the secret and can scrub it.
--
-- The two sibling lookups do not cover this, because they answer "is the row
-- already recorded?" and the dangerous window is exactly the one where it is
-- NOT: startAndPollMigration writes the UPID onto the migration_jobs row
-- (SetMigrationJobStarted) and only then inserts the task_history and audit_log
-- rows. For the few statements in between, a sync tick listing this node finds
-- the task unrecorded and ingests it itself. This lookup catches it because
-- migration_jobs.upid is already set throughout that gap — the same ordering
-- ListRunningTaskHistoryByCluster's anti-join rests on. One documented
-- invariant, two guards, rather than two coincidences.
--
-- The two guards are NOT copies of each other and must stay individually
-- killable: the anti-join guards the reconciler's SELECT, this guards
-- ingestTask's INSERT. Different writes, different call paths, and the tests
-- cross-check that removing either leaves the other's test still passing.
--
-- `upid <> ''` because that is migration_jobs.upid's not-yet-dispatched
-- default; without it a job parked at pending would match a task row that
-- somehow carried an empty UPID.
-- name: ListCrossClusterMigrationUPIDs :many
SELECT upid FROM migration_jobs
WHERE migration_type = 'cross-cluster'
  AND upid <> ''
  AND upid = ANY(sqlc.arg('upids')::text[]);
