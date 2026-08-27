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
      -- Manual mappings are exempt — that is what makes an operator's
      -- decision stick — EXCEPT when the pin has become a lie. A stale pin
      -- re-enters automatic resolution here, in the same atomic statement, so
      -- there is never a moment where it is neither pinned nor resolved.
      --
      -- Three ways a pin goes stale, and each of them is a wrong answer that
      -- nothing else would ever correct:
      AND (
          o.match_method <> 'manual'
          -- The platform is no longer mapped to a cluster. Unmapping is how an
          -- operator REVOKES cluster-scoped visibility of a server's data, and
          -- a pin that kept its cluster_id would go on feeding the coverage
          -- view and the VM detail card of a viewer whose grant was withdrawn.
          OR p.cluster_id IS NULL
          -- The pin names a different cluster than the object's platform does.
          -- A Veeam platformId IS one Proxmox connection, so its objects
          -- cannot belong anywhere else; this catches a platform remapped
          -- underneath an existing pin.
          OR o.cluster_id IS DISTINCT FROM p.cluster_id
          -- The guest holding that VMID is not the guest that was pinned.
          -- Proxmox reuses a VMID once its guest is destroyed, and without
          -- this the replacement silently inherits the old machine's restore
          -- points and reports as protected. Only fires on a genuine identity
          -- change: a churned guest row keeps its guest_smbios entry, and a
          -- rename does not touch the uuid.
          OR EXISTS (
              SELECT 1 FROM guest_smbios g
              WHERE g.cluster_id  = o.cluster_id
                AND g.vmid        = o.vmid
                AND g.smbios_uuid <> ''
                AND o.manual_guest_key <> ''
                AND g.smbios_uuid <> o.manual_guest_key
          )
      )
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

-- ---------------------------------------------------------------------------
-- Veeam's own guests on the cluster.
-- ---------------------------------------------------------------------------

-- name: UpsertVeeamInfrastructure :exec
-- cluster_id and vmid are absent from the UPDATE on purpose: they are resolved
-- by ResolveVeeamInfrastructureGuests and must survive a refresh of the state
-- fields, exactly as veeam_jobs.platform_id does.
INSERT INTO veeam_infrastructure (
    veeam_server_id, veeam_ref, role, name, host_name, is_disabled, is_online, last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (veeam_server_id, veeam_ref)
DO UPDATE SET
    role         = EXCLUDED.role,
    name         = EXCLUDED.name,
    host_name    = EXCLUDED.host_name,
    is_disabled  = EXCLUDED.is_disabled,
    is_online    = EXCLUDED.is_online,
    last_seen_at = now();

-- name: ListVeeamInfrastructureByServer :many
SELECT
    i.id,
    i.veeam_ref,
    i.role,
    i.name,
    i.host_name,
    i.is_disabled,
    i.is_online,
    i.cluster_id,
    i.vmid,
    i.last_seen_at,
    c.name AS cluster_name,
    v.name AS guest_name
FROM veeam_infrastructure i
LEFT JOIN clusters c ON c.id = i.cluster_id
-- The guest is joined at read time on the stable (cluster_id, vmid) identity,
-- never held as a foreign key: the collector re-mints a guest row's UUID on
-- churn. A NULL guest_name means the row resolved to a guest that is mid-churn
-- or gone, which the UI renders as an unlinked name.
LEFT JOIN vms v ON v.cluster_id = i.cluster_id AND v.vmid = i.vmid
WHERE i.veeam_server_id = $1
ORDER BY i.role, i.name;

-- ListVeeamInfrastructureGuestsForCluster is the eligibility feed: which
-- guests on one cluster belong to the Veeam deployment rather than to the
-- workload it protects.
-- name: ListVeeamInfrastructureGuestsForCluster :many
-- Both casts are for sqlc's benefit, not Postgres's: the columns are nullable
-- on the table, so without them callers would handle a pgtype.UUID argument
-- and a pgtype.Int4 result that the WHERE clause already guarantees are set.
-- ORDER BY role as well as vmid, because DISTINCT is on the PAIR and one
-- guest can legitimately hold both roles — the VBR server fills a proxy role
-- itself, and both rows resolve to the same guest by name. Without the second
-- key the reported reason flips between requests. 'backup_server' sorts first
-- and is the more specific fact, which is the one worth showing.
SELECT DISTINCT i.vmid::int AS vmid, i.role
FROM veeam_infrastructure i
WHERE i.cluster_id = @cluster_id::uuid
  AND i.vmid IS NOT NULL
ORDER BY vmid, role;

-- DeleteStaleVeeamInfrastructure prunes rows Veeam has stopped reporting.
--
-- An ordinary grace-windowed sweep, deliberately WITHOUT the empty-listing
-- refusal the catalog sections use. Those guard against VBR's REST service
-- answering before its backup service has loaded the backup catalog; proxies
-- and managed servers are configuration, not catalog, and are not subject to
-- that window. The trade the refusal would make is also the wrong way round
-- here: a spurious empty read costs one interval of a few worker VMs showing
-- as unprotected, while refusing forever means a decommissioned worker stays
-- excluded from coverage permanently and invisibly.
-- Scoped to the roles whose listing was actually READ this pass. The two
-- roles come from two different endpoints, and a caller whose rights stop at
-- the backup catalog can read one and not the other — without the filter,
-- three consecutive failures of the managed-server listing would age the
-- backup server out of the table and put its guest back into the coverage
-- report as a false alarm, with a clean last_sync_at and nothing to explain
-- it. The list must be non-nil: pgx encodes nil as SQL NULL, and
-- role = ANY(NULL) is NULL, which would delete nothing at all.
-- name: DeleteStaleVeeamInfrastructure :exec
DELETE FROM veeam_infrastructure
WHERE veeam_server_id = $1
  AND role = ANY(@roles::text[])
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);

-- ResolveVeeamInfrastructureGuests matches each row to a Proxmox guest.
--
-- Name is the only key there is: neither the proxy state model nor the
-- managed-server model carries an smbios uuid or a vmid. So the match is
-- exact and case-insensitive (the lab has "veeam13-appliance01" beside
-- "Veeam13-appliance02"), never a prefix or substring test — "Veeam" also
-- appears in the name of the VBR server's own guest, and would in any
-- unrelated guest an operator happened to name that way.
--
-- The search is confined to clusters this Veeam server has a mapped platform
-- on, and the match must be UNIQUE across all of them: two guests sharing the
-- name means the key does not identify one, and excluding a guest from
-- coverage on a coin flip would hide a real machine's lack of backups.
-- name: ResolveVeeamInfrastructureGuests :execrows
WITH candidate AS (
    SELECT i.id, lower(i.name) AS name
    FROM veeam_infrastructure i
    WHERE i.veeam_server_id = $1
      -- vms.name is NOT NULL DEFAULT '', so a blank name here would match
      -- every unnamed guest — and where exactly one exists, silently exclude
      -- a real machine from coverage. The deterministic tier of
      -- CorrelateVeeamBackupObjects guards its key the same way.
      AND i.name <> ''
),
resolved AS (
    -- The aggregate always yields exactly one row, so a bare CROSS JOIN
    -- LATERAL keeps every candidate — including the ones that matched
    -- nothing, whose stale resolution must be cleared rather than left.
    SELECT
        c.id,
        CASE WHEN m.matches = 1 THEN m.cluster_id END AS cluster_id,
        CASE WHEN m.matches = 1 THEN m.vmid       END AS vmid
    FROM candidate c
    CROSS JOIN LATERAL (
        SELECT (array_agg(v.cluster_id))[1] AS cluster_id,
               (array_agg(v.vmid))[1]       AS vmid,
               count(*)                     AS matches
        FROM vms v
        WHERE lower(v.name) = c.name
          AND v.type = 'qemu'
          -- EXISTS rather than a join: a Veeam server can map several
          -- platforms onto ONE cluster, and joining would then count the same
          -- guest twice and fail the uniqueness test above for no reason.
          AND EXISTS (
              SELECT 1 FROM veeam_platforms p
              WHERE p.veeam_server_id = $1 AND p.cluster_id = v.cluster_id
          )
    ) m
)
UPDATE veeam_infrastructure i
SET cluster_id = r.cluster_id,
    vmid       = r.vmid
FROM resolved r
WHERE i.id = r.id
  AND (i.cluster_id IS DISTINCT FROM r.cluster_id
    OR i.vmid       IS DISTINCT FROM r.vmid);

-- ---------------------------------------------------------------------------
-- Coverage: what Veeam protection a cluster's guests actually have.
-- ---------------------------------------------------------------------------

-- ListVeeamGuestProtectionForCluster is one row per correlated GUEST, not per
-- backup object.
--
-- The aggregation is the point. A guest appears in as many backup objects as
-- it has backups — daily, weekly and offsite on the lab, up to three — and a
-- naive join would report it three times, each with a third of its restore
-- points and a different "latest backup". RPO is MAX(creation_time) across all
-- of them, and the count is their sum.
-- name: ListVeeamGuestProtectionForCluster :many
WITH obj AS (
    SELECT o.id, o.vmid, o.match_method, o.last_run_failed
    FROM veeam_backup_objects o
    WHERE o.cluster_id = @cluster_id::uuid
      AND o.vmid IS NOT NULL
),
newest AS (
    -- The guest's most recent point across every backup it appears in. Its
    -- malware verdict is the one worth surfacing: an old "Suspicious" that a
    -- later clean backup superseded is history, not a live finding.
    SELECT DISTINCT ON (obj.vmid)
           obj.vmid, rp.creation_time, rp.malware_status
    FROM obj
    JOIN veeam_restore_points rp ON rp.backup_object_id = obj.id
    ORDER BY obj.vmid, rp.creation_time DESC
),
totals AS (
    -- LEFT JOIN, so a guest whose backup object exists but whose points have
    -- all been pruned still appears — with zero. "Veeam knows about this
    -- guest and can restore nothing" is a worse state than never having been
    -- backed up, and it must not be invisible.
    SELECT obj.vmid,
           count(rp.id)                    AS restore_point_count,
           COALESCE(sum(rp.size_bytes), 0) AS restore_point_bytes
    FROM obj
    LEFT JOIN veeam_restore_points rp ON rp.backup_object_id = obj.id
    GROUP BY obj.vmid
)
SELECT
    t.vmid::int                             AS vmid,
    n.creation_time                         AS latest_restore_point,
    COALESCE(n.malware_status, '')::text    AS latest_malware_status,
    t.restore_point_count::bigint           AS restore_point_count,
    t.restore_point_bytes::bigint           AS restore_point_bytes,
    -- The strongest tier that resolved any of this guest's objects: an
    -- operator's own mapping outranks a deterministic match, which outranks a
    -- name guess. Reporting the weakest would flag a guest as low-confidence
    -- on the strength of one stale object.
    (SELECT o2.match_method FROM obj o2
      WHERE o2.vmid = t.vmid
      ORDER BY CASE o2.match_method
                 WHEN 'manual' THEN 0
                 WHEN 'smbios' THEN 1
                 WHEN 'name'   THEN 2
                 ELSE 3
               END
      LIMIT 1)::text                        AS match_method,
    EXISTS (SELECT 1 FROM obj o3 WHERE o3.vmid = t.vmid AND o3.last_run_failed) AS last_run_failed
FROM totals t
LEFT JOIN newest n ON n.vmid = t.vmid
ORDER BY t.vmid;

-- ListVeeamOrphanedObjects returns backup objects whose platform IS mapped to
-- a cluster but which resolve to no guest on it.
--
-- Not an edge case, a feature. These are restore points consuming repository
-- space for machines that no longer exist in the form that was backed up — a
-- deleted guest, a template whose name was reused under a new uuid, a host
-- rebuilt in place. The Veeam console does not call them out, and a coverage
-- view built on name matching would instead report the rebuilt host as
-- protected by its predecessor's backup.
--
-- Objects whose platform is UNMAPPED are deliberately excluded: nothing is
-- known about where they should live, so calling them orphaned would blame the
-- operator's missing mapping on the data.
-- name: ListVeeamOrphanedObjects :many
--
-- The CTEs exist for two reasons. sqlc types a bare scalar subquery as
-- interface{}, and types an AGGREGATE — including through a LATERAL — as NOT
-- NULL, which for max() over an orphan whose restore points have all been
-- pruned means a NULL scanned into a time.Time: a 500 at runtime rather than
-- an error at build time. A plain column reached through a LEFT JOIN to a CTE
-- is typed nullable, correctly.
--
-- They are also CORRELATED to the orphan set rather than to the whole server.
-- Filtering only on veeam_server_id would compute a DISTINCT ON and a GROUP BY
-- over every restore point the server holds — tens of thousands on a real
-- install — before discarding all but the handful of rows this listing
-- typically returns, on every page load.
WITH orphan AS (
    SELECT o.id, o.veeam_object_id, o.smbios_uuid, o.platform_id, o.name,
           o.object_type, o.restore_points_count, o.size_bytes,
           o.last_run_failed, o.last_seen_at, p.cluster_id,
           p.display_name AS platform_name
    FROM veeam_backup_objects o
    JOIN veeam_platforms p
      ON p.veeam_server_id = o.veeam_server_id
     AND p.platform_id     = o.platform_id
     -- Objects on an UNMAPPED platform are excluded: nothing is known about
     -- where they should live, so calling them orphaned would blame the
     -- operator's missing mapping on the data.
     AND p.cluster_id IS NOT NULL
    WHERE o.veeam_server_id = $1
      AND o.match_method = 'none'
),
newest AS (
    SELECT DISTINCT ON (rp.backup_object_id)
           rp.backup_object_id, rp.creation_time
    FROM veeam_restore_points rp
    JOIN orphan ON orphan.id = rp.backup_object_id
    ORDER BY rp.backup_object_id, rp.creation_time DESC
),
sizes AS (
    SELECT rp.backup_object_id,
           COALESCE(sum(rp.size_bytes), 0)::bigint AS restore_point_bytes
    FROM veeam_restore_points rp
    JOIN orphan ON orphan.id = rp.backup_object_id
    GROUP BY rp.backup_object_id
)
SELECT
    o.id,
    o.veeam_object_id,
    o.smbios_uuid,
    o.platform_id,
    o.name,
    o.object_type,
    o.restore_points_count,
    o.size_bytes,
    o.last_run_failed,
    o.last_seen_at,
    o.cluster_id,
    o.platform_name,
    c.name AS cluster_name,
    n.creation_time                              AS latest_restore_point,
    COALESCE(sz.restore_point_bytes, 0)::bigint  AS restore_point_bytes
FROM orphan o
LEFT JOIN clusters c ON c.id = o.cluster_id
LEFT JOIN newest n   ON n.backup_object_id = o.id
LEFT JOIN sizes sz   ON sz.backup_object_id = o.id
ORDER BY o.name;

-- SetVeeamBackupObjectGuest records an operator's own mapping for one backup
-- object, or clears it back to automatic resolution.
--
-- match_method 'manual' is what makes it stick: CorrelateVeeamBackupObjects
-- exempts those rows, so no sync overwrites a human's decision — unless the
-- pin has gone stale, which that query defines and detects.
--
-- manual_guest_key captures the pinned guest's SMBIOS uuid at pin time, which
-- is what the staleness check compares against later. Taken from guest_smbios
-- here rather than passed in, so a caller cannot supply one that does not
-- match the guest they named.
-- name: SetVeeamBackupObjectGuest :one
UPDATE veeam_backup_objects
SET cluster_id   = $3,
    vmid         = $4,
    match_method = CASE WHEN $3::uuid IS NULL THEN 'none' ELSE 'manual' END,
    manual_guest_key = COALESCE(
        (SELECT g.smbios_uuid FROM guest_smbios g
          WHERE g.cluster_id = $3::uuid AND g.vmid = $4::int), '')
WHERE veeam_server_id = $1
  AND id = $2
RETURNING *;

-- name: GetVeeamBackupObject :one
-- One indexed row, so an override does not read every backup object on the
-- server to find the one it is about to change.
SELECT * FROM veeam_backup_objects WHERE veeam_server_id = $1 AND id = $2;

-- name: GetVeeamGuestProtection :one
--
-- ListVeeamGuestProtectionForCluster narrowed to one guest, for the VM detail
-- page's backup card.
--
-- Shaped as joined CTEs rather than scalar subqueries on purpose. sqlc typed
-- `(SELECT creation_time FROM newest)` as a NON-nullable time.Time, which is
-- wrong the moment a guest has no restore points — the commonest interesting
-- case here — and would have failed the scan at runtime rather than at build
-- time. A LEFT JOIN onto a CTE that may be empty types it correctly.
WITH obj AS (
    SELECT o.id, o.match_method, o.last_run_failed
    FROM veeam_backup_objects o
    WHERE o.cluster_id = @cluster_id::uuid
      AND o.vmid = @vmid::int
),
newest AS (
    -- The guest's most recent point across every backup it appears in. Its
    -- malware verdict is the one worth surfacing: an old "Suspicious" that a
    -- later clean backup superseded is history, not a live finding.
    SELECT rp.creation_time, rp.malware_status
    FROM obj
    JOIN veeam_restore_points rp ON rp.backup_object_id = obj.id
    ORDER BY rp.creation_time DESC
    LIMIT 1
),
totals AS (
    -- LEFT JOIN: a guest whose backup object exists but whose points have all
    -- been pruned must still report, with zero.
    SELECT count(rp.id)                            AS restore_point_count,
           COALESCE(sum(rp.size_bytes), 0)::bigint AS restore_point_bytes
    FROM obj
    LEFT JOIN veeam_restore_points rp ON rp.backup_object_id = obj.id
),
meta AS (
    -- Ranked so the STRONGEST tier that resolved any of the guest's objects
    -- wins: reporting the weakest would flag a guest as a low-confidence name
    -- match on the strength of one stale object.
    SELECT count(*) AS object_count,
           COALESCE(min(CASE obj.match_method
                          WHEN 'manual' THEN 0
                          WHEN 'smbios' THEN 1
                          WHEN 'name'   THEN 2
                          ELSE 3
                        END), 3)                   AS method_rank,
           COALESCE(bool_or(obj.last_run_failed), false)::boolean AS last_run_failed
    FROM obj
)
SELECT
    n.creation_time                       AS latest_restore_point,
    COALESCE(n.malware_status, '')::text  AS latest_malware_status,
    m.object_count::bigint                AS object_count,
    t.restore_point_count::bigint         AS restore_point_count,
    t.restore_point_bytes::bigint         AS restore_point_bytes,
    (CASE m.method_rank
       WHEN 0 THEN 'manual'
       WHEN 1 THEN 'smbios'
       WHEN 2 THEN 'name'
       ELSE 'none'
     END)::text                           AS match_method,
    m.last_run_failed                     AS last_run_failed
FROM meta m
-- Both always yield exactly one row (bare aggregates over a possibly-empty
-- set), so the :one contract holds even for a guest Veeam has never seen.
CROSS JOIN totals t
LEFT JOIN newest n ON true;

-- ListVeeamRestorePointsForGuest is the guest's recovery history across every
-- backup it appears in, newest first.
-- name: ListVeeamRestorePointsForGuest :many
SELECT
    rp.id,
    rp.veeam_id,
    rp.name,
    rp.point_type,
    rp.malware_status,
    rp.guest_os_family,
    rp.creation_time,
    rp.size_bytes,
    rp.supports_flr,
    o.name AS object_name
FROM veeam_restore_points rp
JOIN veeam_backup_objects o ON o.id = rp.backup_object_id
WHERE o.cluster_id = @cluster_id::uuid
  AND o.vmid = @vmid::int
ORDER BY rp.creation_time DESC
LIMIT @row_limit::int;

-- ---------------------------------------------------------------------------
-- Alerting.
-- ---------------------------------------------------------------------------

-- GetClusterVeeamRPOStats reports the WORST recovery-point age among the
-- guests on one cluster that Veeam actually protects.
--
-- Deliberately scoped to guests Veeam has a backup object for. A guest Veeam
-- was never meant to protect has no RPO, and evaluating one for it would fire
-- this alert for every unrelated VM on the cluster — the coverage report is
-- what answers "should this guest be backed up at all".
--
-- unrecoverable_count is the count of guests Veeam knows about whose restore
-- points have ALL been pruned. That state is worse than any RPO, but it has no
-- age to measure, so it rides on the alert's message rather than its number:
-- inventing an hours value for it would make every notification a lie.
-- name: GetClusterVeeamRPOStats :one
WITH guest AS (
    SELECT o.vmid, max(rp.creation_time) AS newest
    FROM veeam_backup_objects o
    LEFT JOIN veeam_restore_points rp ON rp.backup_object_id = o.id
    WHERE o.cluster_id = @cluster_id::uuid
      AND o.vmid IS NOT NULL
    GROUP BY o.vmid
),
rated AS (
    SELECT vmid, (extract(epoch FROM now() - newest) / 3600.0)::float8 AS rpo_hours
    FROM guest
    WHERE newest IS NOT NULL
)
-- Always exactly one row, so a cluster where every protected guest has lost
-- its restore points still reports rather than vanishing into ErrNoRows.
SELECT
    COALESCE((SELECT vmid FROM rated ORDER BY rpo_hours DESC LIMIT 1), 0)::int          AS worst_vmid,
    COALESCE((SELECT rpo_hours FROM rated ORDER BY rpo_hours DESC LIMIT 1), 0)::float8  AS worst_rpo_hours,
    -- Counted with the rule's own comparison. Hardcoding ">" made a >= rule
    -- on a guest sitting exactly at the threshold fire with a message reading
    -- "0 of 1 guests over threshold", which is the figure an on-call reader
    -- acts on.
    (SELECT count(*) FROM rated
      WHERE CASE WHEN @inclusive::boolean THEN rpo_hours >= @threshold_hours::float8
                 ELSE rpo_hours > @threshold_hours::float8 END)::bigint          AS over_count,
    (SELECT count(*) FROM guest WHERE newest IS NULL)::bigint                           AS unrecoverable_count,
    (SELECT count(*) FROM guest)::bigint                                                AS protected_count;

-- GetGuestVeeamRPO is the vm-scoped counterpart. protected_count is 0 when
-- Veeam has no backup object for the guest at all, which the engine reads as
-- "nothing to evaluate" rather than as an RPO of zero.
-- name: GetGuestVeeamRPO :one
WITH guest AS (
    SELECT o.id, max(rp.creation_time) AS newest
    FROM veeam_backup_objects o
    LEFT JOIN veeam_restore_points rp ON rp.backup_object_id = o.id
    WHERE o.cluster_id = @cluster_id::uuid
      AND o.vmid = @vmid::int
    GROUP BY o.id
)
SELECT
    COALESCE((SELECT (extract(epoch FROM now() - max(newest)) / 3600.0)::float8
                FROM guest WHERE newest IS NOT NULL), 0)::float8 AS rpo_hours,
    EXISTS (SELECT 1 FROM guest WHERE newest IS NOT NULL)        AS has_restore_point,
    (SELECT count(*) FROM guest)::bigint                          AS protected_count;

-- GetClusterVeeamMalwareStats reports the worst malware verdict across the
-- NEWEST restore point of each guest on a cluster.
--
-- Newest only, on purpose. Veeam records a verdict per restore point, and an
-- old "Suspicious" that a later clean backup superseded is history — alerting
-- on it would keep a resolved finding firing forever with no way to clear it.
--
-- The verdict is mapped to a number because alert rules compare numerically:
-- Clean 0, Informative 1, Suspicious 2, Infected 3, anything unrecognised 0.
-- A rule of `>= 2` catches Suspicious and worse. Note that Veeam's inline
-- encryption detection marks a LOT of points Suspicious — 76 of 172 on the
-- lab — so operators watching for confirmed findings will want `>= 3`.
-- name: GetClusterVeeamMalwareStats :one
WITH newest AS (
    SELECT DISTINCT ON (o.vmid) o.vmid, rp.malware_status
    FROM veeam_backup_objects o
    JOIN veeam_restore_points rp ON rp.backup_object_id = o.id
    WHERE o.cluster_id = @cluster_id::uuid
      AND o.vmid IS NOT NULL
    ORDER BY o.vmid, rp.creation_time DESC
),
rated AS (
    SELECT vmid, malware_status,
           CASE malware_status
             WHEN 'Infected'    THEN 3
             WHEN 'Suspicious'  THEN 2
             WHEN 'Informative' THEN 1
             ELSE 0
           END AS severity
    FROM newest
)
SELECT
    COALESCE((SELECT vmid FROM rated ORDER BY severity DESC, vmid LIMIT 1), 0)::int              AS worst_vmid,
    COALESCE((SELECT malware_status FROM rated ORDER BY severity DESC, vmid LIMIT 1), '')::text  AS worst_status,
    COALESCE((SELECT severity FROM rated ORDER BY severity DESC, vmid LIMIT 1), 0)::float8       AS worst_severity,
    (SELECT count(*) FROM rated
      WHERE CASE WHEN @inclusive::boolean THEN severity >= @threshold_severity::float8
                 ELSE severity > @threshold_severity::float8 END)::bigint                     AS over_count,
    (SELECT count(*) FROM rated)::bigint                                                          AS scanned_count;

-- GetGuestVeeamMalware is the vm-scoped counterpart of
-- GetClusterVeeamMalwareStats.
-- name: GetGuestVeeamMalware :one
WITH newest AS (
    SELECT rp.malware_status
    FROM veeam_backup_objects o
    JOIN veeam_restore_points rp ON rp.backup_object_id = o.id
    WHERE o.cluster_id = @cluster_id::uuid
      AND o.vmid = @vmid::int
    ORDER BY rp.creation_time DESC
    LIMIT 1
)
SELECT
    COALESCE((SELECT malware_status FROM newest), '')::text AS status,
    COALESCE((SELECT CASE malware_status
                       WHEN 'Infected'    THEN 3
                       WHEN 'Suspicious'  THEN 2
                       WHEN 'Informative' THEN 1
                       ELSE 0
                     END FROM newest), 0)::float8           AS severity,
    EXISTS (SELECT 1 FROM newest)                           AS scanned;

-- GetVeeamRepositoryUsageStats reports the fullest repository across every
-- Veeam server.
--
-- Global by nature: one repository holds the backups of every cluster its
-- server protects, so there is no cluster to attribute the number to.
--
-- Repositories reporting zero capacity are excluded throughout. Veeam reports
-- that for targets whose size it cannot measure — the lab's object-store
-- repository is one — and dividing by it is both a crash and a meaningless
-- 0%-full reading that would mask a real repository beside it.
-- name: GetVeeamRepositoryUsageStats :one
WITH measured AS (
    SELECT name, (used_bytes::float8 * 100.0 / capacity_bytes) AS used_percent
    FROM veeam_repositories
    WHERE capacity_bytes > 0
)
SELECT
    COALESCE((SELECT name FROM measured ORDER BY used_percent DESC LIMIT 1), '')::text        AS fullest_name,
    COALESCE((SELECT used_percent FROM measured ORDER BY used_percent DESC LIMIT 1), 0)::float8 AS fullest_percent,
    (SELECT count(*) FROM measured
      WHERE CASE WHEN @inclusive::boolean THEN used_percent >= @threshold_percent::float8
                 ELSE used_percent > @threshold_percent::float8 END)::bigint                AS over_count,
    (SELECT count(*) FROM measured)::bigint                                                    AS measured_count;
