-- name: CreateMigrationJob :one
INSERT INTO migration_jobs (
    source_cluster_id, target_cluster_id, source_node, target_node,
    vmid, vm_type, migration_type,
    storage_map, network_map,
    online, bwlimit_kib, delete_source, target_vmid,
    created_by, migration_mode, target_storage
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
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

-- name: UpdateMigrationJobStatus :exec
UPDATE migration_jobs
SET status = $2, error_message = $3
WHERE id = $1;

-- name: UpdateMigrationJobProgress :exec
UPDATE migration_jobs
SET progress = $2, upid = $3
WHERE id = $1;

-- name: UpdateMigrationJobChecks :exec
UPDATE migration_jobs
SET check_results = $2, status = $3
WHERE id = $1;

-- name: CompleteMigrationJob :exec
UPDATE migration_jobs
SET status = $2, completed_at = $3, error_message = $4
WHERE id = $1;

-- name: SetMigrationJobStarted :exec
UPDATE migration_jobs
SET status = 'migrating', started_at = $2, upid = $3
WHERE id = $1;

-- name: CancelMigrationJob :exec
UPDATE migration_jobs
SET status = 'cancelled'
WHERE id = $1 AND status IN ('pending', 'checking');
