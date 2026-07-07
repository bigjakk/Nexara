-- name: InsertRollingUpdateJob :one
INSERT INTO rolling_update_jobs (cluster_id, parallelism, reboot_after_update, auto_restore_guests, package_excludes, ha_policy, ha_warnings, auto_upgrade, created_by, notify_channel_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetRollingUpdateJob :one
SELECT * FROM rolling_update_jobs WHERE id = $1;

-- name: ListRollingUpdateJobs :many
SELECT * FROM rolling_update_jobs
WHERE cluster_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdateRollingUpdateJobStatus :exec
UPDATE rolling_update_jobs
SET status = $2, updated_at = now()
WHERE id = $1;

-- name: StartRollingUpdateJob :exec
UPDATE rolling_update_jobs
SET status = 'running', started_at = now(), updated_at = now()
WHERE id = $1 AND status = 'pending';

-- Terminal transitions set cleanup_pending: the job may still hold cluster
-- state (DRS pause, native CRS pause, disabled HA rules, stopped passthrough
-- guests). The orchestrator's cleanup sweep re-checks flagged jobs until
-- every held resource is confirmed released, then clears the flag.

-- name: CompleteRollingUpdateJob :execrows
UPDATE rolling_update_jobs
SET status = 'completed', cleanup_pending = true, completed_at = now(), updated_at = now()
WHERE id = $1 AND status IN ('pending', 'running', 'paused');

-- name: FailRollingUpdateJob :execrows
UPDATE rolling_update_jobs
SET status = 'failed', failure_reason = $2, cleanup_pending = true, completed_at = now(), updated_at = now()
WHERE id = $1 AND status IN ('pending', 'running', 'paused');

-- name: CancelRollingUpdateJob :execrows
UPDATE rolling_update_jobs
SET status = 'cancelled', cleanup_pending = true, completed_at = now(), updated_at = now()
WHERE id = $1 AND status IN ('pending', 'running', 'paused');

-- name: PauseRollingUpdateJob :exec
UPDATE rolling_update_jobs
SET status = 'paused', updated_at = now()
WHERE id = $1 AND status = 'running';

-- name: ResumeRollingUpdateJob :exec
UPDATE rolling_update_jobs
SET status = 'running', updated_at = now()
WHERE id = $1 AND status = 'paused';

-- name: HasRunningJobForCluster :one
SELECT EXISTS(
    SELECT 1 FROM rolling_update_jobs
    WHERE cluster_id = $1 AND status IN ('pending', 'running', 'paused')
) AS has_running;

-- name: ListRunningRollingUpdateJobs :many
SELECT * FROM rolling_update_jobs
WHERE status = 'running'
ORDER BY created_at;

-- name: InsertRollingUpdateNode :one
INSERT INTO rolling_update_nodes (job_id, node_name, node_order, packages_json)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRollingUpdateNode :one
SELECT * FROM rolling_update_nodes WHERE id = $1;

-- name: ListRollingUpdateNodes :many
SELECT * FROM rolling_update_nodes
WHERE job_id = $1
ORDER BY node_order;

-- name: UpdateRollingUpdateNodeStep :exec
UPDATE rolling_update_nodes
SET step = $2, updated_at = now()
WHERE id = $1;

-- name: FailRollingUpdateNode :execrows
UPDATE rolling_update_nodes
SET step = 'failed', failure_reason = $2, updated_at = now()
WHERE id = $1 AND step NOT IN ('completed', 'failed', 'skipped');

-- name: SkipRollingUpdateNode :execrows
UPDATE rolling_update_nodes
SET step = 'skipped', skip_reason = $2, updated_at = now()
WHERE id = $1 AND step = 'pending';

-- name: SkipRollingUpdateNodeAny :exec
UPDATE rolling_update_nodes
SET step = 'skipped', skip_reason = $2, updated_at = now()
WHERE id = $1;

-- name: SetNodeDrainStarted :exec
UPDATE rolling_update_nodes
SET step = 'draining', drain_started_at = now(), updated_at = now()
WHERE id = $1;

-- name: SetNodeDrainCompletedManual :exec
UPDATE rolling_update_nodes
SET drain_completed_at = now(), step = 'awaiting_upgrade', updated_at = now()
WHERE id = $1;

-- name: SetNodeDrainCompletedAuto :exec
UPDATE rolling_update_nodes
SET drain_completed_at = now(), step = 'upgrading', updated_at = now()
WHERE id = $1;

-- name: SetNodePackagesJSON :exec
UPDATE rolling_update_nodes
SET packages_json = $2, updated_at = now()
WHERE id = $1;

-- name: SetNodeGuestsJSON :exec
UPDATE rolling_update_nodes
SET guests_json = $2, updated_at = now()
WHERE id = $1;

-- name: ConfirmNodeUpgrade :exec
UPDATE rolling_update_nodes
SET upgrade_confirmed_at = now(), updated_at = now()
WHERE id = $1 AND step = 'awaiting_upgrade';

-- name: SetNodeUpgradeStarted :exec
-- COALESCE preserves the original upgrade_started_at on a resume re-launch
-- (after a Swarm reschedule or K8s rolling restart killed the previous
-- SSH goroutine) so the absolute upgrade timeout in advanceUpgrading
-- measures from the first launch attempt, not the most recent.
UPDATE rolling_update_nodes
SET upgrade_started_at = COALESCE(upgrade_started_at, now()), updated_at = now()
WHERE id = $1;

-- name: SetNodeUpgradeCompleted :exec
UPDATE rolling_update_nodes
SET step = 'rebooting', upgrade_completed_at = now(), upgrade_confirmed_at = now(), reboot_started_at = now(), updated_at = now()
WHERE id = $1;

-- name: SetNodeUpgradeCompletedNoReboot :exec
UPDATE rolling_update_nodes
SET step = 'health_check', upgrade_completed_at = now(), upgrade_confirmed_at = now(), health_check_at = now(), updated_at = now()
WHERE id = $1;

-- name: SetNodeUpgradeOutput :exec
UPDATE rolling_update_nodes
SET upgrade_output = $2, updated_at = now()
WHERE id = $1;

-- name: SetNodeRebootStarted :exec
UPDATE rolling_update_nodes
SET step = 'rebooting', reboot_started_at = now(), updated_at = now()
WHERE id = $1;

-- name: SetNodeRebootCompleted :exec
UPDATE rolling_update_nodes
SET reboot_completed_at = now(), updated_at = now()
WHERE id = $1;

-- name: SetNodeHealthCheckPassed :exec
UPDATE rolling_update_nodes
SET step = 'health_check', health_check_at = now(), updated_at = now()
WHERE id = $1;

-- name: SetNodeRestoreStarted :exec
UPDATE rolling_update_nodes
SET step = 'restoring', restore_started_at = now(), updated_at = now()
WHERE id = $1;

-- name: SetNodeRestoreCompleted :exec
UPDATE rolling_update_nodes
SET step = 'completed', restore_completed_at = now(), updated_at = now()
WHERE id = $1;

-- name: CountActiveNodes :one
SELECT COUNT(*) AS active_count
FROM rolling_update_nodes
WHERE job_id = $1 AND step IN ('draining', 'awaiting_upgrade', 'upgrading', 'rebooting', 'health_check', 'restoring');

-- name: CountCompletedNodes :one
SELECT
    COUNT(*) FILTER (WHERE step = 'completed') AS completed,
    COUNT(*) FILTER (WHERE step = 'failed') AS failed,
    COUNT(*) FILTER (WHERE step = 'skipped') AS skipped,
    COUNT(*) AS total
FROM rolling_update_nodes
WHERE job_id = $1;

-- Legacy: new code records disabled HA rules at job scope (see
-- SetJobDisabledHARules); this remains only so the release path can clear
-- per-node records written by pre-000073 versions.
-- name: SetNodeDisabledHARules :exec
UPDATE rolling_update_nodes
SET disabled_ha_rules = $2, updated_at = now()
WHERE id = $1;

-- name: SetJobDisabledHARules :exec
UPDATE rolling_update_jobs
SET disabled_ha_rules = $2, updated_at = now()
WHERE id = $1;

-- name: ClearJobNativeCRSPaused :exec
UPDATE rolling_update_jobs
SET native_crs_paused = false, updated_at = now()
WHERE id = $1;

-- name: SetNodeStoppedPassthrough :exec
UPDATE rolling_update_nodes
SET stopped_passthrough_json = $2, updated_at = now()
WHERE id = $1;

-- ClearJobCleanupPending is self-guarding: the flag only clears when no
-- job-level marker and no node-level record still holds state, so a release
-- racing a concurrent record-write cannot retire the job from the sweep
-- while something is still held. (Node columns left NULL by pre-000073
-- versions don't block the clear — the release path materializes them to
-- explicit lists on its first pass.)
-- name: ClearJobCleanupPending :exec
UPDATE rolling_update_jobs
SET cleanup_pending = false, updated_at = now()
WHERE rolling_update_jobs.id = $1
  AND rolling_update_jobs.drs_was_enabled = false
  AND rolling_update_jobs.native_crs_paused = false
  AND (rolling_update_jobs.disabled_ha_rules IS NULL OR rolling_update_jobs.disabled_ha_rules::text IN ('null', '[]'))
  AND NOT EXISTS (
      SELECT 1 FROM rolling_update_nodes n
      WHERE n.job_id = rolling_update_jobs.id
        AND ((n.disabled_ha_rules IS NOT NULL AND n.disabled_ha_rules::text NOT IN ('null', '[]'))
          OR (n.stopped_passthrough_json IS NOT NULL AND n.stopped_passthrough_json::text NOT IN ('null', '[]')))
  );

-- ArmJobCleanupPending re-arms the sweep when held state is recorded onto a
-- job that already reached a terminal status (the startNode cancel race);
-- a no-op for active jobs, whose terminal transition sets the flag itself.
-- name: ArmJobCleanupPending :exec
UPDATE rolling_update_jobs
SET cleanup_pending = true, updated_at = now()
WHERE id = $1 AND status IN ('completed', 'failed', 'cancelled');

-- name: IncrementJobCleanupAttempts :one
UPDATE rolling_update_jobs
SET cleanup_attempts = cleanup_attempts + 1, updated_at = now()
WHERE id = $1
RETURNING cleanup_attempts;

-- name: ListRollingUpdateJobsNeedingCleanup :many
SELECT * FROM rolling_update_jobs
WHERE cleanup_pending = true
  AND status IN ('completed', 'failed', 'cancelled')
ORDER BY updated_at
LIMIT 20;

-- name: ListCleanupPendingJobsForCluster :many
SELECT * FROM rolling_update_jobs
WHERE cluster_id = $1
  AND cleanup_pending = true
  AND status IN ('completed', 'failed', 'cancelled')
ORDER BY updated_at;

-- name: SetJobDRSWasEnabled :exec
UPDATE rolling_update_jobs
SET drs_was_enabled = $2, updated_at = now()
WHERE id = $1;

-- name: SetJobNativeCRSPaused :exec
UPDATE rolling_update_jobs
SET native_crs_paused = true, saved_crs_config = $2, updated_at = now()
WHERE id = $1;

-- name: TouchRollingUpdateNode :exec
UPDATE rolling_update_nodes
SET updated_at = now()
WHERE id = $1;

-- name: GetNextPendingNode :one
SELECT * FROM rolling_update_nodes
WHERE job_id = $1 AND step = 'pending'
ORDER BY node_order
LIMIT 1;
