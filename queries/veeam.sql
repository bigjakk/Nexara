-- name: CreateVeeamServer :one
INSERT INTO veeam_servers (
    name, base_url, username, password_encrypted,
    api_revision, product_version, license_edition,
    tls_fingerprint, verify_tls, enabled
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetVeeamServer :one
SELECT * FROM veeam_servers WHERE id = $1;

-- name: ListVeeamServers :many
SELECT * FROM veeam_servers ORDER BY created_at DESC;

-- name: UpdateVeeamServer :one
UPDATE veeam_servers
SET name = $2,
    base_url = $3,
    username = $4,
    password_encrypted = $5,
    api_revision = $6,
    product_version = $7,
    license_edition = $8,
    tls_fingerprint = $9,
    verify_tls = $10,
    enabled = $11
WHERE id = $1
RETURNING *;

-- name: DeleteVeeamServer :exec
DELETE FROM veeam_servers WHERE id = $1;

-- name: ListActiveVeeamServers :many
SELECT * FROM veeam_servers WHERE enabled = true ORDER BY created_at ASC;

-- name: SetVeeamServerSyncCompleted :exec
-- Stamps a completed sync and sets the note in one statement.
--
-- The note is usually empty, but a pass can succeed WITH a caveat — it read
-- everything and then refused to prune on an empty listing, say. Clearing the
-- error unconditionally here is what erased that caveat in the same pass that
-- raised it.
UPDATE veeam_servers
SET last_sync_at = now(),
    last_sync_error = $2
WHERE id = $1;

-- name: SetVeeamServerSyncError :exec
-- Deliberately does NOT touch last_sync_at. A server that has not synced in a
-- week must not report "last synced: 30 seconds ago" beside its error.
UPDATE veeam_servers
SET last_sync_error = $2
WHERE id = $1;

-- name: UpsertVeeamPlatform :exec
INSERT INTO veeam_platforms (veeam_server_id, platform_id, display_name, last_seen_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (veeam_server_id, platform_id)
DO UPDATE SET
    -- display_name only ever moves forward: the license workload list is the
    -- only source of a human label, and a sync that could not read it must not
    -- blank out one an earlier sync found.
    display_name = CASE WHEN EXCLUDED.display_name <> '' THEN EXCLUDED.display_name
                        ELSE veeam_platforms.display_name END,
    last_seen_at = now();

-- name: ListVeeamPlatformsByServer :many
SELECT * FROM veeam_platforms WHERE veeam_server_id = $1 ORDER BY display_name, platform_id;

-- name: UpsertVeeamRepository :exec
INSERT INTO veeam_repositories (
    veeam_server_id, veeam_id, name, repo_type, host_name, path,
    capacity_bytes, free_bytes, used_bytes, is_online, is_out_of_date, last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
ON CONFLICT (veeam_server_id, veeam_id)
DO UPDATE SET
    name = EXCLUDED.name,
    repo_type = EXCLUDED.repo_type,
    host_name = EXCLUDED.host_name,
    path = EXCLUDED.path,
    capacity_bytes = EXCLUDED.capacity_bytes,
    free_bytes = EXCLUDED.free_bytes,
    used_bytes = EXCLUDED.used_bytes,
    is_online = EXCLUDED.is_online,
    is_out_of_date = EXCLUDED.is_out_of_date,
    last_seen_at = now();

-- name: ListVeeamRepositoriesByServer :many
SELECT * FROM veeam_repositories WHERE veeam_server_id = $1 ORDER BY name;

-- name: DeleteStaleVeeamRepositories :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStalePBSSnapshots).
--
-- Both sides of the comparison come from now(), so a Postgres running even a
-- second behind the Nexara container cannot make rows this very pass wrote
-- look stale. The grace window absorbs a momentary non-observation on top.
DELETE FROM veeam_repositories
WHERE veeam_server_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);

-- name: InsertVeeamRepositoryMetric :exec
INSERT INTO veeam_repository_metrics (
    time, veeam_server_id, repository_veeam_id, capacity_bytes, free_bytes, used_bytes
)
VALUES (now(), $1, $2, $3, $4, $5);

-- name: GetVeeamRepositoryMetrics :many
SELECT bucket::timestamptz AS time, capacity_bytes, free_bytes, used_bytes
FROM veeam_repository_metrics_5m
WHERE veeam_server_id = $1
  AND repository_veeam_id = $2
  AND bucket >= $3
ORDER BY bucket;

-- name: UpsertVeeamJob :exec
-- platform_id is deliberately absent from the UPDATE below: it is derived from
-- sessions by DeriveVeeamJobPlatforms and must survive a job-state refresh.
INSERT INTO veeam_jobs (
    veeam_server_id, veeam_id, name, job_type, workload, description,
    status, last_result, last_run, next_run, next_run_policy,
    repository_veeam_id, repository_name, objects_count, last_session_id,
    progress_percent, bottleneck, duration, processing_rate,
    processed_size, read_size, transferred_size, last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
        $16, $17, $18, $19, $20, $21, $22, now())
ON CONFLICT (veeam_server_id, veeam_id)
DO UPDATE SET
    name = EXCLUDED.name,
    job_type = EXCLUDED.job_type,
    workload = EXCLUDED.workload,
    description = EXCLUDED.description,
    status = EXCLUDED.status,
    last_result = EXCLUDED.last_result,
    last_run = EXCLUDED.last_run,
    next_run = EXCLUDED.next_run,
    next_run_policy = EXCLUDED.next_run_policy,
    repository_veeam_id = EXCLUDED.repository_veeam_id,
    repository_name = EXCLUDED.repository_name,
    objects_count = EXCLUDED.objects_count,
    last_session_id = EXCLUDED.last_session_id,
    progress_percent = EXCLUDED.progress_percent,
    bottleneck = EXCLUDED.bottleneck,
    duration = EXCLUDED.duration,
    processing_rate = EXCLUDED.processing_rate,
    processed_size = EXCLUDED.processed_size,
    read_size = EXCLUDED.read_size,
    transferred_size = EXCLUDED.transferred_size,
    last_seen_at = now();

-- name: DeriveVeeamJobPlatforms :exec
-- Job states carry no platformId; sessions are the only bridge. STICKY by
-- construction — the WHERE clause only touches rows that have none yet, so a
-- pruned session cannot un-attribute a job that was already resolved.
UPDATE veeam_jobs j
SET platform_id = s.platform_id
FROM (
    SELECT DISTINCT ON (job_veeam_id) job_veeam_id, platform_id
    FROM veeam_sessions
    WHERE veeam_server_id = $1
      AND job_veeam_id IS NOT NULL
      AND platform_id IS NOT NULL
    ORDER BY job_veeam_id, creation_time DESC
) s
WHERE j.veeam_server_id = $1
  AND j.veeam_id = s.job_veeam_id
  AND j.platform_id IS NULL;

-- name: ListVeeamJobsByServer :many
SELECT * FROM veeam_jobs WHERE veeam_server_id = $1 ORDER BY name;

-- name: DeleteStaleVeeamJobs :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStalePBSSnapshots).
--
-- Both sides of the comparison come from now(), so a Postgres running even a
-- second behind the Nexara container cannot make rows this very pass wrote
-- look stale. The grace window absorbs a momentary non-observation on top.
DELETE FROM veeam_jobs
WHERE veeam_server_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);

-- name: UpsertVeeamBackupObject :one
INSERT INTO veeam_backup_objects (
    veeam_server_id, veeam_object_id, smbios_uuid, platform_id, name,
    object_type, backup_ref, restore_points_count, size_bytes, last_run_failed, last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
ON CONFLICT (veeam_server_id, veeam_object_id)
DO UPDATE SET
    smbios_uuid = EXCLUDED.smbios_uuid,
    platform_id = EXCLUDED.platform_id,
    name = EXCLUDED.name,
    object_type = EXCLUDED.object_type,
    backup_ref = EXCLUDED.backup_ref,
    restore_points_count = EXCLUDED.restore_points_count,
    size_bytes = EXCLUDED.size_bytes,
    last_run_failed = EXCLUDED.last_run_failed,
    last_seen_at = now()
RETURNING *;

-- name: ListVeeamBackupObjectsByServer :many
SELECT * FROM veeam_backup_objects WHERE veeam_server_id = $1 ORDER BY name;

-- name: DeleteStaleVeeamBackupObjects :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStalePBSSnapshots).
--
-- Both sides of the comparison come from now(), so a Postgres running even a
-- second behind the Nexara container cannot make rows this very pass wrote
-- look stale. The grace window absorbs a momentary non-observation on top.
DELETE FROM veeam_backup_objects
WHERE veeam_server_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);

-- name: UpsertVeeamRestorePoint :exec
INSERT INTO veeam_restore_points (
    veeam_server_id, backup_object_id, veeam_id, name, point_type,
    malware_status, guest_os_family, creation_time, size_bytes,
    backup_id, session_id, backup_file_id, supports_flr, last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())
ON CONFLICT (veeam_server_id, veeam_id)
DO UPDATE SET
    backup_object_id = EXCLUDED.backup_object_id,
    name = EXCLUDED.name,
    point_type = EXCLUDED.point_type,
    malware_status = EXCLUDED.malware_status,
    guest_os_family = EXCLUDED.guest_os_family,
    creation_time = EXCLUDED.creation_time,
    size_bytes = EXCLUDED.size_bytes,
    backup_id = EXCLUDED.backup_id,
    session_id = EXCLUDED.session_id,
    backup_file_id = EXCLUDED.backup_file_id,
    supports_flr = EXCLUDED.supports_flr,
    last_seen_at = now();

-- name: ListVeeamRestorePointsByObject :many
SELECT * FROM veeam_restore_points
WHERE backup_object_id = $1
ORDER BY creation_time DESC;

-- name: ListVeeamRestorePointsByServer :many
SELECT * FROM veeam_restore_points
WHERE veeam_server_id = $1
ORDER BY creation_time DESC
LIMIT $2;

-- name: PruneVeeamRestorePoints :exec
-- Prunes on last_seen_at, NEVER on creation_time: a restore point Veeam still
-- holds must not disappear from Nexara because it is old, or every RPO and
-- coverage figure derived from it silently becomes wrong. This deletes only
-- rows Veeam has stopped reporting.
DELETE FROM veeam_restore_points
WHERE veeam_server_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);

-- name: UpsertVeeamSession :exec
-- nexara_initiated is absent from the UPDATE below: it is set once by the
-- handler that started or stopped the job and must survive every later poll
-- of that session.
INSERT INTO veeam_sessions (
    veeam_server_id, veeam_id, job_veeam_id, name, session_type,
    platform_name, platform_id, state, result, result_message, is_canceled,
    algorithm, bottleneck, duration, processing_rate,
    processed_size, read_size, transferred_size, progress_percent,
    creation_time, end_time, initiated_by, last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
        $16, $17, $18, $19, $20, $21, $22, now())
ON CONFLICT (veeam_server_id, veeam_id)
DO UPDATE SET
    job_veeam_id = EXCLUDED.job_veeam_id,
    name = EXCLUDED.name,
    session_type = EXCLUDED.session_type,
    platform_name = EXCLUDED.platform_name,
    platform_id = EXCLUDED.platform_id,
    state = EXCLUDED.state,
    result = EXCLUDED.result,
    result_message = EXCLUDED.result_message,
    is_canceled = EXCLUDED.is_canceled,
    algorithm = EXCLUDED.algorithm,
    bottleneck = EXCLUDED.bottleneck,
    duration = EXCLUDED.duration,
    processing_rate = EXCLUDED.processing_rate,
    processed_size = EXCLUDED.processed_size,
    read_size = EXCLUDED.read_size,
    transferred_size = EXCLUDED.transferred_size,
    progress_percent = EXCLUDED.progress_percent,
    creation_time = EXCLUDED.creation_time,
    end_time = EXCLUDED.end_time,
    initiated_by = EXCLUDED.initiated_by,
    last_seen_at = now();

-- name: GetVeeamSessionWatermark :one
-- The newest session already stored, used as the createdAfterFilter for the
-- next poll.
--
-- The newest ROW rather than MAX(creation_time): an aggregate over an empty
-- table is SQL NULL, which sqlc types as interface{} and pgx cannot scan into
-- a time. Reading the row instead returns pgx.ErrNoRows on an empty table,
-- which is an explicit "first sync" the caller handles — and it is served
-- straight off idx_veeam_sessions_server_time.
SELECT creation_time FROM veeam_sessions
WHERE veeam_server_id = $1
ORDER BY creation_time DESC
LIMIT 1;

-- name: ListVeeamSessionsByServer :many
-- Scoped in SQL, not in Go, because of the LIMIT: filtering after the limit
-- would take the newest N rows server-wide and then discard the ones the
-- caller cannot see, so a busy cluster's runs would crowd out a quiet
-- cluster's entirely and the caller would see an empty list.
--
-- A NULL platform_ids means "no restriction" (a global holder). A non-NULL
-- array restricts to those platforms AND excludes rows with no platform at
-- all — an unattributable row could belong to any cluster, so it is
-- global-only. pgx sends a nil slice as NULL and a non-nil empty slice as
-- '{}', and that distinction is what makes both cases work.
SELECT * FROM veeam_sessions
WHERE veeam_server_id = $1
  AND (
    sqlc.narg(platform_ids)::uuid[] IS NULL
    OR (platform_id IS NOT NULL AND platform_id = ANY(sqlc.narg(platform_ids)::uuid[]))
  )
ORDER BY creation_time DESC
LIMIT sqlc.arg(row_limit);

-- name: PruneVeeamSessions :exec
-- Sessions are historical events and Veeam keeps tens of thousands of them, so
-- this DOES prune on creation_time: Nexara mirrors a bounded recent window by
-- design rather than the server's full history.
DELETE FROM veeam_sessions
WHERE veeam_server_id = $1 AND creation_time < $2;
