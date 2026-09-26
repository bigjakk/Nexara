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
--
-- 'running' is this claim's marker and nothing else: it means the scheduler is
-- starting the run, and the due predicate below reads it as "in flight". A run
-- whose Proxmox task is still going is 'dispatched' (see
-- ReconcileDispatchedScheduledTasks), which that predicate does not treat as
-- in flight, so a long snapshot never holds its row out of its next slot.
-- The claim clears last_error and last_upid, which belong to the previous run:
-- without that, a run being started would show beside the last run's error.
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
    last_error  = NULL,
    last_upid   = NULL,
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
--
-- last_upid rides along for the same reason: a run that reached Proxmox is
-- parked as 'dispatched' with its UPID, and ReconcileDispatchedScheduledTasks
-- settles it later exactly as it settles an enabled row, keeping the reason in
-- last_error.
-- name: DisableScheduledTaskForBadSchedule :exec
UPDATE scheduled_tasks
SET enabled     = false,
    last_run_at = now(),
    next_run_at = NULL,
    last_status = $2,
    last_error  = $3,
    last_upid   = $4,
    updated_at  = now()
WHERE id = $1;

-- UpdateTaskLastRun records how a run left the scheduler: 'dispatched' with the
-- UPID in last_upid, or 'failed' with the reason in last_error and no UPID.
-- name: UpdateTaskLastRun :exec
UPDATE scheduled_tasks
SET last_run_at = $2, next_run_at = $3, last_status = $4, last_error = $5, last_upid = $6, updated_at = now()
WHERE id = $1;

-- ReconcileDispatchedScheduledTasks settles every dispatched run whose Proxmox
-- task has finished, from the task_history row the collector keeps for its
-- UPID. It is the other half of UpdateTaskLastRun: dispatching a run records
-- its UPID and nothing more, because Proxmox refuses much of what it refuses
-- inside the forked worker ("snapshot name '…' already used", "snapshot
-- feature is not available"), after the call that started the task has
-- already returned.
--
-- Success or failure is task_history.status as the collector writes it
-- (classifyTaskExit in internal/collector, which applies proxmox.TaskSucceeded:
-- '' / 'OK' / 'WARNINGS: N' are 'completed'), not a second reading of
-- exit_status. Only 'completed' and 'failed' settle a run. Nothing else
-- finalizes a task any other way: 'stopped', or an empty status, comes only
-- from a hand-made PUT /api/v1/tasks/:upid, and a row whose task holds one
-- stays dispatched until the schedule's next run replaces it.
--
-- The three last_status values are parameters so that the Go side names each
-- one once: the scheduler writes dispatched_status through UpdateTaskLastRun
-- and passes the same constant here.
--
-- Which rows it may touch:
--   * only last_status = dispatched_status. The claim's 'running' is a run
--     being started whose UPID is not known yet, and a settled row is settled:
--     matching it again would append the exit status to last_error on every
--     tick.
--   * only the task that row's OWN run dispatched (th.upid = st.last_upid).
--     When a schedule fires again before its previous run's task has finished
--     and been settled, the claim clears the old UPID and the new run records
--     its own: the status follows the newer run, the older run's task can no
--     longer overwrite it, and that older outcome stays in task_history
--     (Events → Tasks) only.
--   * only a task on the schedule's own cluster. upid is unique in
--     task_history, but a UPID names a node, not a cluster, so the cluster is
--     compared as well.
-- The row is found however it got into task_history: the scheduler's own
-- insert, or, when that insert failed, the collector's later ingest of the same
-- UPID as an external task (source 'proxmox', same cluster).
--
-- last_error: a failed task's exit status, falling back to a fixed text when
-- the row has none (only a hand-made write can leave one empty).
--
-- One exit status is not Proxmox's. The collector writes 'vanished' when it
-- has had no status for a task for staleTaskGrace (24 h: the node rebooted,
-- the task log was rotated), and the rest of Nexara reads that as "lost
-- track", not as a failure — the failed-task alerts and the digest skip it
-- (queries/tasks.sql). It still settles the run here, because leaving it
-- dispatched would show Running until the next run, a week away on a weekly
-- schedule. It settles as 'failed', which is what task_history says too, but
-- with a message saying the outcome is unknown rather than the bare sentinel.
-- TestScheduleReconcileWordsTheCollectorsGiveUp (internal/collector) pins the
-- sentinel and the hours to the collector's own.
--
-- The message is put in FRONT of whatever last_error already holds rather
-- than replacing it. For a row UpdateTaskLastRun dispatched that is NULL, so
-- the result is the message alone. For a row DisableScheduledTaskForBadSchedule
-- parked it is the reason the schedule was disabled, which must survive the
-- run settling — the result is "<message>; disabled: …", the shape
-- parkUnschedulableTask gives a run that failed before dispatch. A task that
-- succeeded leaves last_error as it is.
--
-- Returns the settled rows so the scheduler can log and announce each one.
-- name: ReconcileDispatchedScheduledTasks :many
UPDATE scheduled_tasks st
SET last_status = CASE th.status
                      WHEN 'completed' THEN sqlc.arg('succeeded_status')::text
                      ELSE sqlc.arg('failed_status')::text
                  END,
    last_error  = CASE th.status
                      WHEN 'failed' THEN concat_ws('; ',
                          CASE th.exit_status
                              WHEN 'vanished' THEN 'Proxmox never reported how this task ended; Nexara stopped following it after 24 hours'
                              WHEN '' THEN 'Proxmox task failed'
                              ELSE th.exit_status
                          END,
                          st.last_error)
                      ELSE st.last_error
                  END,
    updated_at  = now()
FROM task_history th
WHERE st.last_status = sqlc.arg('dispatched_status')::text
  AND th.upid = st.last_upid
  AND th.cluster_id = st.cluster_id
  AND th.status IN ('completed', 'failed')
RETURNING st.id, st.cluster_id, st.last_status, st.last_error;
