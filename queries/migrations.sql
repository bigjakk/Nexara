-- name: CreateMigrationJob :one
INSERT INTO migration_jobs (
    source_cluster_id, target_cluster_id, source_node, target_node,
    vmid, vm_type, migration_type,
    storage_map, network_map,
    online, bwlimit_kib, delete_source, target_vmid,
    created_by, migration_mode, target_storage, disk_format
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
RETURNING *;

-- name: GetMigrationJob :one
SELECT * FROM migration_jobs WHERE id = $1;

-- ListMigrationJobs backs the Migrations page. accessible_cluster_ids carries
-- the caller's view:migration RBAC scope: NULL means global access (no
-- restriction); an array restricts rows ('{}' matches nothing).
--
-- A migration straddles two clusters, and the rule — matching what
-- MigrationHandler.List enforces per row — is that visibility on EITHER end is
-- enough to know the job exists. Hence the OR across both columns rather than
-- a single membership test. Both columns are NOT NULL, so unlike the alert and
-- audit scopes there is no three-valued-logic case to reason about here.
--
-- Applied in SQL because LIMIT/OFFSET run before the per-row trim, so paging
-- over every cluster's jobs and filtering afterwards gives a scoped caller
-- short pages with holes in them.
-- name: ListMigrationJobs :many
SELECT * FROM migration_jobs
WHERE (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR source_cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[])
       OR target_cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListMigrationJobsByCluster :many
SELECT * FROM migration_jobs
WHERE source_cluster_id = $1 OR target_cluster_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: BeginMigrationJobCheck :execrows
-- A pre-flight check starts only on a job that has not started: pending, or
-- checking while another check runs or after one that never finished. A job
-- that has started, failed, completed or been cancelled keeps its status (a
-- check once put a cancelled job back to pending).
UPDATE migration_jobs
SET status = 'checking'
WHERE id = $1 AND status IN ('pending', 'checking');

-- name: UpdateMigrationJobProgress :exec
UPDATE migration_jobs
SET progress = $2, upid = $3
WHERE id = $1;

-- name: SaveMigrationJobChecks :execrows
-- A check's report moves the job on only from checking: a job started, or
-- checked by another request, while this check ran keeps what it became (a
-- check once wrote pending over a migrating job, which a cancel then took).
UPDATE migration_jobs
SET check_results = $2, status = $3
WHERE id = $1 AND status = 'checking';

-- name: AbandonMigrationJobCheck :exec
-- A check that could not run leaves the job pending, unless it moved on.
UPDATE migration_jobs
SET status = 'pending'
WHERE id = $1 AND status = 'checking';

-- name: CompleteMigrationJob :exec
UPDATE migration_jobs
SET status = $2, completed_at = $3, error_message = $4
WHERE id = $1;

-- name: SetMigrationJobStarted :exec
UPDATE migration_jobs
SET status = 'migrating', started_at = $2, upid = $3
WHERE id = $1;

-- name: ClaimMigrationJob :execrows
-- Execute's claim: only one request starts a job, and from here a cancel finds
-- it started, although the orchestrator records the task only once Proxmox
-- has answered. Its heartbeat starts here (BeatMigrationJob).
UPDATE migration_jobs
SET status = 'migrating', heartbeat_at = now()
WHERE id = $1 AND status IN ('pending', 'checking');

-- name: BeatMigrationJob :exec
-- A running job's heartbeat, stamped by the instance that follows it; see
-- migration.(*Orchestrator).ReconcileLapsed.
UPDATE migration_jobs
SET heartbeat_at = now()
WHERE id = $1 AND status = 'migrating';

-- name: ListLapsedMigrationJobs :many
-- Jobs still "migrating" that no instance follows: their heartbeat stopped, or
-- one claimed by a release without heartbeats has gone untouched far longer
-- than any run takes. Database time throughout, as the heartbeat is.
SELECT * FROM migration_jobs
WHERE status = 'migrating'
  AND ((heartbeat_at IS NOT NULL AND heartbeat_at < now() - make_interval(secs => sqlc.arg(lapse_secs)::float8))
    OR (heartbeat_at IS NULL AND updated_at < now() - make_interval(secs => sqlc.arg(unbeaten_secs)::float8)))
ORDER BY created_at;

-- name: FailLapsedMigrationJob :execrows
-- ListLapsedMigrationJobs' test again, at the write: a job whose instance has
-- stamped it since it was listed is left running.
UPDATE migration_jobs
SET status = 'failed', completed_at = now(), error_message = sqlc.arg(error_message)
WHERE id = sqlc.arg(id) AND status = 'migrating'
  AND ((heartbeat_at IS NOT NULL AND heartbeat_at < now() - make_interval(secs => sqlc.arg(lapse_secs)::float8))
    OR (heartbeat_at IS NULL AND updated_at < now() - make_interval(secs => sqlc.arg(unbeaten_secs)::float8)));

-- name: ResetLapsedMigrationChecks :execrows
-- Checks cut off with their instance: a check is one request, so one begun
-- this long ago ended without saving. Pending again, as a check that could not
-- run is (AbandonMigrationJobCheck).
UPDATE migration_jobs
SET status = 'pending'
WHERE status = 'checking' AND updated_at < now() - make_interval(secs => sqlc.arg(lapse_secs)::float8);

-- name: CancelMigrationJob :execrows
-- Only a pending job: a claimed job is already on its way to Proxmox, and a
-- job being checked is pending or failed again once its check has saved.
UPDATE migration_jobs
SET status = 'cancelled'
WHERE id = $1 AND status = 'pending';
