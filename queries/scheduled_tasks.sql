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

-- A scheduled task is addressed by its uuid, which says nothing about which
-- cluster owns it, while the permission gate on these two resolves the cluster
-- from the request PATH. Without the cluster_id predicate, `WHERE id = $1` let
-- a caller holding manage:schedule on one cluster rewrite or delete another's
-- row. The handler re-reads the row and compares its cluster as well
-- (taskInCluster in internal/api/handlers/schedules.go); the two layers fail
-- independently, and either one alone refuses the request.
--
-- :execrows rather than :exec so a zero-row result is visible: if the handler
-- check is ever dropped, the write still cannot happen AND the caller is told,
-- rather than the endpoint reporting success for a row it never touched.

-- name: UpdateScheduledTask :execrows
UPDATE scheduled_tasks
SET schedule = sqlc.arg('schedule'),
    params = sqlc.arg('params'),
    enabled = sqlc.arg('enabled'),
    updated_at = now()
WHERE id = sqlc.arg('id')
  AND cluster_id = sqlc.arg('cluster_id');

-- name: DeleteScheduledTask :execrows
DELETE FROM scheduled_tasks
WHERE id = sqlc.arg('id')
  AND cluster_id = sqlc.arg('cluster_id');

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

-- DisableScheduledTaskForBadSchedule parks a task whose cron can never fire.
--
-- Leaving it enabled is the busy loop: this table's due predicate counts NULL
-- next_run_at as "due now", and a cron that never comes round has no other
-- value to write — so the row would be claimed, RUN, and re-queued on every
-- tick, repeating whatever action it carries. Disabling makes it inert while
-- last_error says why, and next_run_at NULL means that fixing the expression
-- and re-enabling runs it once, promptly, instead of waiting for a slot the
-- old expression never had.
--
-- last_status is a parameter rather than a literal 'failed' because it
-- describes the RUN, not the schedule: a task can execute perfectly and still
-- have an expression that can never come round again, and recording that run
-- as a failure would send the operator looking for a problem in the wrong half.
-- name: DisableScheduledTaskForBadSchedule :exec
UPDATE scheduled_tasks
SET enabled     = false,
    last_run_at = now(),
    next_run_at = NULL,
    last_status = $2,
    last_error  = $3,
    updated_at  = now()
WHERE id = $1;

-- name: UpdateTaskLastRun :exec
UPDATE scheduled_tasks
SET last_run_at = $2, next_run_at = $3, last_status = $4, last_error = $5, updated_at = now()
WHERE id = $1;
