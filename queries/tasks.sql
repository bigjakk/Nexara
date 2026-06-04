-- name: InsertTaskHistory :one
INSERT INTO task_history (cluster_id, user_id, upid, description, status, node, task_type)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (upid) DO NOTHING
RETURNING *;

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
