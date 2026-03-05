-- name: InsertScheduledTask :one
INSERT INTO scheduled_tasks (cluster_id, resource_type, resource_id, node, action, schedule, params, enabled, next_run_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListScheduledTasksByCluster :many
SELECT * FROM scheduled_tasks
WHERE cluster_id = $1
ORDER BY created_at DESC;

-- name: GetScheduledTask :one
SELECT * FROM scheduled_tasks WHERE id = $1;

-- name: UpdateScheduledTask :exec
UPDATE scheduled_tasks
SET schedule = $2, params = $3, enabled = $4, updated_at = now()
WHERE id = $1;

-- name: DeleteScheduledTask :exec
DELETE FROM scheduled_tasks WHERE id = $1;

-- name: ListDueTasks :many
SELECT * FROM scheduled_tasks
WHERE enabled = true AND (next_run_at IS NULL OR next_run_at <= now());

-- name: UpdateTaskLastRun :exec
UPDATE scheduled_tasks
SET last_run_at = $2, next_run_at = $3, last_status = $4, last_error = $5, updated_at = now()
WHERE id = $1;
