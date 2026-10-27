-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Re-files the audit rows of storages, PBS datastores and ingested storage
-- tasks that can be resolved under the identity rows written from this release
-- on use, so that a storage's or a PBS server's History reads its rows from one
-- place rather than from three spellings of the key (see "Storage and backup
-- identity on an audit entry" in docs/api-reference.md).
--
-- Rewrites resource_type and resource_id and, for an ingested imgdel or
-- prunebackups, action (to the names the storage rows written from now on use).
-- Who did it, when, and what the details say are untouched: where it moves
-- resource_id off the value that named the object it adds the details keys a
-- new row carries (storage, datastore, resource_name, job_id) beside the
-- existing ones, which are never changed.
--
-- Every statement that changes a row records the originals in the same UPDATE,
-- as details.rekeyed_from {resource_type, resource_id, action}; a row that
-- already carries one keeps it. Nothing is deleted, and every statement matches
-- only the OLD shape, so a second run changes nothing. If it is interrupted the
-- migration is one transaction and leaves nothing behind.
--
-- What it does NOT touch, because the old row does not say enough:
--   - a storage update or delete row keyed by an inventory row that no longer
--     exists and naming no storage in its details;
--   - a PBS row whose datastore (or job) name is not held by exactly one PBS
--     server of all of them: none known to hold it, or several, or the one
--     holder is newer than the row;
--   - backup_job_created rows keyed "backup-job" (the id Proxmox gave the job
--     was never recorded), and everything already keyed by what it names.

-- A storage row keyed by the storage_pools row the request addressed, while that
-- row still exists. The collector prunes and re-creates those rows, so most no
-- longer do.
UPDATE audit_log a
SET resource_id = sp.storage,
    details = jsonb_build_object('storage', sp.storage) || CASE WHEN a.details ? 'rekeyed_from' THEN a.details ELSE a.details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', a.resource_type, 'resource_id', a.resource_id, 'action', a.action)) END
FROM storage_pools sp
WHERE a.resource_type = 'storage'
  AND a.resource_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND sp.id::text = a.resource_id
  AND sp.cluster_id = a.cluster_id
  AND jsonb_typeof(a.details) = 'object';

-- The same, for a row whose inventory row is gone but whose details name the
-- storage (upload, delete_content, pull_oci, download_url, download_appliance).
UPDATE audit_log
SET resource_id = details->>'storage',
    details = CASE WHEN details ? 'rekeyed_from' THEN details ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type = 'storage'
  AND resource_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND jsonb_typeof(details) = 'object'
  AND COALESCE(details->>'storage', '') <> ''
  AND details->>'storage' <> resource_id;

-- A virtio-win config saved with no storage chosen is the cluster's alone, as
-- the rows written from now on file it.
UPDATE audit_log
SET resource_type = 'cluster',
    resource_id = cluster_id::text,
    details = CASE WHEN details ? 'rekeyed_from' THEN details ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type = 'storage'
  AND action = 'virtio_win_config_update'
  AND resource_id = ''
  AND cluster_id IS NOT NULL
  AND jsonb_typeof(details) = 'object';

-- Uploads, volume copies (imgcopy) and URL downloads run outside Nexara: the
-- task names no storage, only the node that ran it, so the row is the node's.
UPDATE audit_log
SET resource_type = 'node',
    resource_id = details->>'node',
    details = CASE WHEN details ? 'rekeyed_from' THEN details ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type = 'storage'
  AND source = 'proxmox'
  AND jsonb_typeof(details) = 'object'
  AND details->>'task_type' IN ('imgcopy', 'download')
  AND COALESCE(details->>'node', '') <> '';

-- A volume deleted (imgdel) or backups pruned (prunebackups) outside Nexara,
-- filed as the storage actions delete_content and prune_backups. The storage is
-- the end of the UPID's worker id, "[<owner vmid>@]<storage>" for both; a row
-- whose UPID disagrees with its resource_id is left alone.
UPDATE audit_log
SET resource_type = 'storage',
    action = CASE action WHEN 'imgdel' THEN 'delete_content' ELSE 'prune_backups' END,
    resource_id = regexp_replace(resource_id, '^.*@', ''),
    details = jsonb_build_object('storage', regexp_replace(resource_id, '^.*@', '')) || CASE WHEN details ? 'rekeyed_from' THEN details ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type = 'proxmox_task'
  AND source = 'proxmox'
  AND action IN ('imgdel', 'prunebackups')
  AND jsonb_typeof(details) = 'object'
  AND split_part(details->>'upid', ':', 7) = resource_id
  AND regexp_replace(resource_id, '^.*@', '') <> '';

-- A PBS datastore action (garbage collection, prune, snapshot delete, protect,
-- notes), filed as "backup" under the datastore's name or the snapshot's
-- "store/type/id", with no server. The server is the one PBS server, of ALL of
-- them, known to hold a datastore of that name (the row's cluster is not
-- compared: a server moves between clusters and becomes standalone when its
-- cluster is removed, so the cluster it has now says nothing about the one it
-- had), provided it already existed when the row was written; any other count
-- leaves the row as it is. The old resource_id was exactly the label the
-- activity feed showed, so it becomes resource_name.
WITH known AS (
    SELECT pbs_server_id, datastore FROM pbs_snapshots GROUP BY 1, 2
    UNION SELECT pbs_server_id, datastore FROM pbs_datastore_metrics GROUP BY 1, 2
    UNION SELECT pbs_server_id, store FROM pbs_sync_jobs
    UNION SELECT pbs_server_id, store FROM pbs_verify_jobs
), holders AS (
    SELECT datastore, count(*) AS servers, (array_agg(pbs_server_id))[1] AS server_id
    FROM known GROUP BY datastore
), legacy AS (
    SELECT a.id, a.created_at, a.resource_id AS label,
           CASE WHEN a.action IN ('gc_triggered', 'datastore_pruned')
                THEN COALESCE(NULLIF(a.details->>'store', ''), a.resource_id)
                ELSE COALESCE(NULLIF(a.details->>'store', ''), split_part(a.resource_id, '/', 1))
           END AS datastore
    FROM audit_log a
    WHERE a.resource_type = 'backup'
      AND a.action IN ('gc_triggered', 'datastore_pruned', 'snapshot_deleted', 'snapshot_protected',
                       'snapshot_unprotected', 'snapshot_notes_updated')
      AND jsonb_typeof(a.details) = 'object'
), owned AS (
    SELECT l.id, l.label, l.datastore, h.server_id
    FROM legacy l
    JOIN holders h ON h.datastore = l.datastore AND h.servers = 1
    JOIN pbs_servers s ON s.id = h.server_id AND s.created_at <= l.created_at
)
UPDATE audit_log a
SET resource_type = 'pbs_server',
    resource_id = o.server_id::text,
    details = jsonb_build_object('datastore', o.datastore, 'resource_name', o.label) || CASE WHEN a.details ? 'rekeyed_from' THEN a.details ELSE a.details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', a.resource_type, 'resource_id', a.resource_id, 'action', a.action)) END
FROM owned o
WHERE a.id = o.id;

-- A PBS sync or verify job triggered, filed as "backup" under the job's id. The
-- server is the one PBS server, of ALL of them, that has a job of that id and
-- kind among the jobs the collector synced (same reasoning as above: no cluster
-- comparison), provided it already existed when the row was written; the
-- datastore the job works on comes with it.
WITH jobs AS (
    SELECT 'sync_job_triggered' AS action, pbs_server_id, job_id, store FROM pbs_sync_jobs
    UNION ALL
    SELECT 'verify_job_triggered', pbs_server_id, job_id, store FROM pbs_verify_jobs
), holders AS (
    SELECT action, job_id, count(*) AS servers, (array_agg(pbs_server_id))[1] AS server_id, (array_agg(store))[1] AS datastore
    FROM jobs GROUP BY action, job_id
), owned AS (
    SELECT a.id, a.resource_id AS job_id, h.server_id, h.datastore
    FROM audit_log a
    JOIN holders h ON h.action = a.action AND h.job_id = a.resource_id AND h.servers = 1
    JOIN pbs_servers s ON s.id = h.server_id AND s.created_at <= a.created_at
    WHERE a.resource_type = 'backup'
      AND a.action IN ('sync_job_triggered', 'verify_job_triggered')
      AND jsonb_typeof(a.details) = 'object'
)
UPDATE audit_log a
SET resource_type = 'pbs_server',
    resource_id = o.server_id::text,
    details = jsonb_build_object('job_id', o.job_id, 'resource_name', o.job_id, 'datastore', o.datastore) || CASE WHEN a.details ? 'rekeyed_from' THEN a.details ELSE a.details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', a.resource_type, 'resource_id', a.resource_id, 'action', a.action)) END
FROM owned o
WHERE a.id = o.id;
