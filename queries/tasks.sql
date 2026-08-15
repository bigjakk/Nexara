-- Both insert queries derive vmid from the UPID id field (field 7 of
-- UPID:node:pid:pstart:starttime:type:id:user@realm:) in SQL — the same
-- expression migration 000076 used to backfill — so no insert path can
-- forget it. Non-guest tasks (empty/non-numeric id) store NULL; the {1,9}
-- bound (VMIDs cap at 999999999) keeps a pathological all-numeric id from
-- overflowing the ::int cast.
-- name: InsertTaskHistory :one
INSERT INTO task_history (cluster_id, user_id, upid, description, status, node, task_type, vmid)
VALUES ($1, $2, $3, $4, $5, $6, $7,
    CASE WHEN split_part($3, ':', 7) ~ '^[0-9]{1,9}$'
         THEN split_part($3, ':', 7)::int END)
ON CONFLICT (upid) DO NOTHING
RETURNING *;

-- InsertExternalTaskHistory records a PVE-native (non-Nexara) task discovered by
-- the collector. Unlike InsertTaskHistory it sets started_at/finished_at/
-- exit_status explicitly, so a task already finished when first seen is stored
-- fully-formed (and a still-running one as status='running'). Attributed to the
-- system user; ON CONFLICT keeps it idempotent across sync ticks.
-- name: InsertExternalTaskHistory :exec
INSERT INTO task_history (
    cluster_id, user_id, upid, description, status, exit_status,
    node, task_type, started_at, finished_at, source, vmid
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'proxmox',
    CASE WHEN split_part($3, ':', 7) ~ '^[0-9]{1,9}$'
         THEN split_part($3, ':', 7)::int END)
ON CONFLICT (upid) DO NOTHING;

-- name: UpdateTaskHistory :exec
UPDATE task_history
SET status = $2, exit_status = $3, progress = $4, finished_at = $5
WHERE upid = $1;

-- name: ListTaskHistory :many
SELECT * FROM task_history
WHERE user_id = $1
ORDER BY started_at DESC
LIMIT $2;

-- name: ListAllTaskHistory :many
SELECT * FROM task_history
ORDER BY started_at DESC
LIMIT $1;

-- name: ListTaskHistoryByCluster :many
SELECT * FROM task_history
WHERE cluster_id = $1
ORDER BY started_at DESC
LIMIT $2;

-- name: GetTaskByUpid :one
SELECT * FROM task_history
WHERE upid = $1
LIMIT 1;

-- DeleteCompletedTasks removes terminal task_history rows whose finish (or
-- start, if never finalized) predates the caller-supplied cutoff. Never deletes
-- a still-running row — the status guard keeps long disk-moves/migrations in the
-- source of truth. Cutoff is computed in Go (now - retention) so the window is
-- configurable (TASK_HISTORY_RETENTION); shared by the automatic scheduler sweep
-- and the manual Clear-Completed endpoint.
-- name: DeleteCompletedTasks :exec
DELETE FROM task_history
WHERE status != 'running' AND COALESCE(finished_at, started_at) < sqlc.arg('cutoff')::timestamptz;

-- name: ListRunningTaskHistoryByCluster :many
SELECT * FROM task_history
WHERE cluster_id = $1 AND status = 'running';

-- ReconcileTaskHistory marks a still-running task terminal. Scoped to
-- status='running' so it never clobbers rows already finalized by the
-- migration orchestrator / DRS executor. :execrows lets the caller emit a
-- task_update event only when a row actually flipped.
-- name: ReconcileTaskHistory :execrows
UPDATE task_history
SET status = $2, exit_status = $3, finished_at = $4, updated_at = now()
WHERE upid = $1 AND status = 'running';

-- ListTaskHistoryFiltered backs the Tasks page: optional cluster_id + status +
-- vmids filters with offset pagination. Mirrors ListAuditLogAdvanced. NULL
-- narg = no filter on that column. vmids matches the guest VMID parsed from
-- the UPID at insert (folder detail view passes a folder's VMID set).
-- accessible_cluster_ids carries the caller's view:task RBAC scope: NULL means
-- global access (no restriction); an array restricts rows — and the Total the
-- count query feeds into pagination — to those clusters ('{}' matches nothing).
-- name: ListTaskHistoryFiltered :many
SELECT * FROM task_history
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('status')::text   IS NULL OR status     = sqlc.narg('status'))
  AND (sqlc.narg('vmids')::int[]   IS NULL OR vmid       = ANY(sqlc.narg('vmids')::int[]))
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
ORDER BY started_at DESC
LIMIT $1 OFFSET $2;

-- CountTaskHistoryFiltered returns the total matching the same filters, for the
-- Tasks page pagination. Mirrors CountAuditLogAdvanced. Must stay filter-for-filter in
-- sync with ListTaskHistoryFiltered — in particular accessible_cluster_ids, or
-- the Total leaks other clusters' task counts to scoped users.
-- name: CountTaskHistoryFiltered :one
SELECT count(*) FROM task_history
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('status')::text   IS NULL OR status     = sqlc.narg('status'))
  AND (sqlc.narg('vmids')::int[]   IS NULL OR vmid       = ANY(sqlc.narg('vmids')::int[]))
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]));
