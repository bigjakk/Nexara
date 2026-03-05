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

-- name: ListTaskHistoryByCluster :many
SELECT * FROM task_history
WHERE cluster_id = $1
ORDER BY started_at DESC
LIMIT $2;

-- name: DeleteCompletedTasks :exec
DELETE FROM task_history
WHERE user_id = $1
  AND (status != 'running' OR started_at < NOW() - INTERVAL '1 hour');
