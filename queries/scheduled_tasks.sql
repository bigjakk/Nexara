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

-- name: ClaimDueTasks :many
-- Atomically claims due tasks so concurrent schedulers (e.g. during leader
-- takeover) don't double-run the same row. SKIP LOCKED makes each
-- competing transaction return a disjoint set without blocking. Inside the
-- claim we mark last_status='running' and bump next_run_at past the
-- guard window so even after the transaction commits the row won't
-- re-match the due predicate until either (a) the run finishes and the
-- caller writes the real next_run_at via UpdateTaskLastRun or (b) the
-- claim goes stale (caller crashed) and the stale-recovery branch picks
-- it up again. stale_seconds = reclaim threshold for crashed claimants;
-- guard_seconds = how far to bump next_run_at upfront.
WITH due AS (
    SELECT id FROM scheduled_tasks
    WHERE enabled = true
      AND (next_run_at IS NULL OR next_run_at <= now())
      AND (
          last_status IS DISTINCT FROM 'running'
          OR last_run_at IS NULL
          OR last_run_at < now() - make_interval(secs => sqlc.arg('stale_seconds')::float)
      )
    FOR UPDATE SKIP LOCKED
)
UPDATE scheduled_tasks st
SET last_status = 'running',
    last_run_at = now(),
    next_run_at = now() + make_interval(secs => sqlc.arg('guard_seconds')::float),
    updated_at  = now()
FROM due
WHERE st.id = due.id
RETURNING st.*;

-- name: UpdateTaskLastRun :exec
UPDATE scheduled_tasks
SET last_run_at = $2, next_run_at = $3, last_status = $4, last_error = $5, updated_at = now()
WHERE id = $1;
