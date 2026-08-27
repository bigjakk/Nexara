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
-- cluster_id is set ONLY on first discovery, and only when the install has
-- exactly one active cluster — there is then no second cluster whose data a
-- wrong guess could expose.
--
-- Doing it here rather than as a recurring "map anything still NULL" sweep is
-- the point: unmapping a platform sets cluster_id back to NULL, so a sweep
-- would silently re-map it on the next tick and undo an operator's deliberate
-- revocation of cluster-scoped access within minutes. The ON CONFLICT branch
-- below never touches cluster_id, so once the row exists the mapping is the
-- operator's alone.
--
-- The subquery cannot error on a multi-cluster install: the count guard is
-- inside it, so it yields no rows — and therefore NULL — rather than "more
-- than one row returned by a subquery".
INSERT INTO veeam_platforms (veeam_server_id, platform_id, display_name, cluster_id, last_seen_at)
VALUES (
    $1, $2, $3,
    (SELECT c.id FROM clusters c
      WHERE c.is_active
        AND (SELECT count(*) FROM clusters WHERE is_active) = 1),
    now())
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

-- ---------------------------------------------------------------------------
-- Phase 3: platform mapping and guest correlation.
-- ---------------------------------------------------------------------------

-- name: GetVeeamPlatform :one
SELECT * FROM veeam_platforms WHERE veeam_server_id = $1 AND platform_id = $2;

-- ListVeeamPlatformsWithCluster feeds the mapping UI: every Proxmox connection
-- the server has been seen protecting, with the Nexara cluster (if any) an
-- operator has attached it to. object_count is what makes an unmapped platform
-- legible — "18 guests you cannot see yet" rather than a bare UUID.
-- name: ListVeeamPlatformsWithCluster :many
SELECT
    p.veeam_server_id,
    p.platform_id,
    p.display_name,
    p.cluster_id,
    p.last_seen_at,
    c.name AS cluster_name,
    (SELECT count(*) FROM veeam_backup_objects o
      WHERE o.veeam_server_id = p.veeam_server_id
        AND o.platform_id = p.platform_id) AS object_count
FROM veeam_platforms p
LEFT JOIN clusters c ON c.id = p.cluster_id
WHERE p.veeam_server_id = $1
ORDER BY p.display_name, p.platform_id;

-- SetVeeamPlatformCluster attaches (or, with NULL, detaches) a Proxmox
-- connection from a Nexara cluster. This is the operator-confirmed mapping
-- every cluster-scoped Veeam permission resolves through, which is why it is
-- deliberately not derived: Veeam exposes no field that names the Nexara
-- cluster, and guessing wrong would show one tenant's backups to another.
-- name: SetVeeamPlatformCluster :one
UPDATE veeam_platforms
SET cluster_id = $3
WHERE veeam_server_id = $1 AND platform_id = $2
RETURNING *;

-- CorrelateVeeamBackupObjects resolves every backup object on one server to a
-- (cluster_id, vmid) guest, in one statement.
--
-- One statement, not a reset-then-match sequence, on purpose: clearing every
-- correlation and rebuilding it would leave a window — however brief — in
-- which the coverage view reports every guest unprotected. In a backup product
-- that is the single worst thing a transient state can say, so the new value
-- is computed and written atomically instead.
--
-- Tiers, in order:
--
--   smbios — the guest's smbios1 uuid equals Veeam's objectId. Deterministic.
--   name   — a single QEMU guest in the mapped cluster carries that name AND
--            the collector has affirmatively recorded that the guest has no
--            SMBIOS uuid (a guest_smbios row with an empty smbios_uuid). Low
--            confidence, and flagged as such.
--
--            That second condition is load-bearing, and it is a positive
--            requirement rather than the absence of a row on purpose. Three
--            states have to be told apart:
--
--              guest has a uuid          → the smbios tier is the only tier.
--                A cached uuid the tier did not match is not an unknown, it
--                is a positive MISMATCH — commonly a host rebuilt in place,
--                which keeps its name, gets a new uuid, and leaves Veeam
--                holding the replaced machine's backup under the old one.
--                Name-matching there reports the replacement as protected by
--                a backup of the machine it replaced, the single failure this
--                whole design exists to prevent.
--              guest has no uuid         → nothing deterministic exists, so a
--                flagged name match is the best honest answer.
--              guest not yet scanned     → we do not know which of the two it
--                is, and must say so. Requiring the affirmative record is
--                what stops a platform mapped seconds ago — before the SMBIOS
--                pass has visited its cluster — from name-matching every
--                object it has, orphans included.
--
--            An AMBIGUOUS name (two guests, which the lab already has)
--            resolves to nothing rather than to whichever row sorted first.
--   none   — unresolved. Either the platform is not mapped to a cluster yet,
--            or the object is orphaned: its guest no longer exists in the form
--            that was backed up. That is a feature, not a gap — see the
--            orphaned-objects listing.
--
-- Rows an operator has mapped by hand (match_method 'manual') are excluded
-- entirely, and the final predicate skips rows whose resolution has not
-- changed so a quiet pass does not churn updated_at on every object.
-- name: CorrelateVeeamBackupObjects :execrows
WITH candidate AS (
    SELECT
        o.id,
        o.name,
        lower(o.smbios_uuid) AS smbios_uuid,
        p.cluster_id
    FROM veeam_backup_objects o
    LEFT JOIN veeam_platforms p
           ON p.veeam_server_id = o.veeam_server_id
          AND p.platform_id     = o.platform_id
    WHERE o.veeam_server_id = $1
      AND o.match_method <> 'manual'
),
by_smbios AS (
    -- Grouped with the same "exactly one" rule the name tier uses. The uuid is
    -- supposed to be unique within a cluster, but nothing in Proxmox enforces
    -- it — smbios1 is operator-settable, and a guest built by copying another
    -- one's config carries its uuid. Two guests holding the same uuid makes
    -- the deterministic key non-deterministic, and without the HAVING the
    -- UPDATE below would silently pick whichever row the planner handed it.
    -- Resolving to nothing surfaces the object as needing a manual mapping,
    -- which is the honest answer.
    SELECT c.id, c.cluster_id, min(g.vmid) AS vmid
    FROM candidate c
    JOIN guest_smbios g
      ON g.cluster_id  = c.cluster_id
     AND g.smbios_uuid = c.smbios_uuid
    WHERE c.smbios_uuid <> ''
    GROUP BY c.id, c.cluster_id
    HAVING count(*) = 1
),
by_name AS (
    SELECT c.id, c.cluster_id, min(v.vmid) AS vmid
    FROM candidate c
    JOIN vms v
      ON v.cluster_id = c.cluster_id
     AND lower(v.name) = lower(c.name)
     AND v.type = 'qemu'
    -- The affirmative "this guest has no SMBIOS uuid" record. An inner join,
    -- so a guest the collector has never scanned is excluded rather than
    -- treated as uuid-less.
    JOIN guest_smbios gn
      ON gn.cluster_id = v.cluster_id
     AND gn.vmid = v.vmid
     AND gn.smbios_uuid = ''
    WHERE NOT EXISTS (SELECT 1 FROM by_smbios s WHERE s.id = c.id)
    GROUP BY c.id, c.cluster_id
    HAVING count(*) = 1
),
resolved AS (
    SELECT
        c.id,
        COALESCE(s.cluster_id, n.cluster_id) AS cluster_id,
        COALESCE(s.vmid, n.vmid)             AS vmid,
        CASE
            WHEN s.id IS NOT NULL THEN 'smbios'
            WHEN n.id IS NOT NULL THEN 'name'
            ELSE 'none'
        END                                  AS match_method
    FROM candidate c
    LEFT JOIN by_smbios s ON s.id = c.id
    LEFT JOIN by_name   n ON n.id = c.id
)
UPDATE veeam_backup_objects o
SET cluster_id   = r.cluster_id,
    vmid         = r.vmid,
    match_method = r.match_method
FROM resolved r
WHERE o.id = r.id
  AND (o.cluster_id   IS DISTINCT FROM r.cluster_id
    OR o.vmid         IS DISTINCT FROM r.vmid
    OR o.match_method IS DISTINCT FROM r.match_method);
