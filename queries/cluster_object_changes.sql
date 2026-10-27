-- Changes to the cluster page's objects that reach no audit row (migrations/000124,
-- 000125): a replication job edited in the Proxmox GUI, an OSD that went down, a
-- pool whose size was changed there. Keyed as audit_log is — cluster_id,
-- resource_type, resource_id — so a History reads both with one selector.

-- MarkClusterObjectBaseline says a cluster's objects of one kind have been read
-- at least once; the replication trigger records an object appearing only after.
-- name: MarkClusterObjectBaseline :exec
INSERT INTO cluster_object_baselines (cluster_id, resource_type)
VALUES ($1, $2)
ON CONFLICT (cluster_id, resource_type) DO NOTHING;

-- InsertClusterObjectChange records one change a writer saw — the Ceph pass diffing
-- two readings, the observer diffing Proxmox's configuration against Nexara's;
-- the replication trigger writes its own rows. last_seen_at is when the object
-- was last seen as it was; vmid is set for a guest only; fields are the names of
-- the fields that differ (empty for added and removed).
-- name: InsertClusterObjectChange :exec
INSERT INTO cluster_object_changes (cluster_id, resource_type, resource_id, vmid, change, fields, detail, node, observed_by, last_seen_at)
VALUES (sqlc.arg('cluster_id'), sqlc.arg('resource_type'), sqlc.arg('resource_id'), sqlc.narg('vmid')::int,
        sqlc.arg('change'), sqlc.arg('fields')::text[], sqlc.arg('detail')::jsonb, sqlc.arg('node'),
        sqlc.arg('observed_by'), sqlc.arg('last_seen_at'));

-- ClassifyClusterObjectChanges decides, once, whether something already on
-- record explains each settled change: an audit row for the same object —
-- Nexara's, or a Proxmox task the collector ingested, which files under the same
-- key — made after the object was last seen as it was (less 2 minutes for how
-- stale that sighting can be) and no later than the change was seen (plus 30
-- seconds of clock skew between Nexara and the node). A task counts while it
-- runs: its audit row is made at the start, and a Ceph OSD takes minutes to
-- appear. Only a change classified false is shown.
--
-- A new OSD cannot be keyed by its number in the task that made it (the
-- cephcreateosd UPID names the device), so an OSD that appeared is also
-- explained by an osd_create on its host.
--
-- "Settled" is the caller's settled_before: a change seen more recently stays
-- unclassified, and hidden, until the collector has had time to ingest the
-- Proxmox task that may explain it. A change on a node waits for that node's
-- task list (listed_nodes) until give_up_before; an object no node owns has no
-- list to wait for.
-- name: ClassifyClusterObjectChanges :execrows
UPDATE cluster_object_changes c
SET explained = EXISTS (
    SELECT 1
    FROM audit_log a
    LEFT JOIN task_history th ON th.cluster_id = a.cluster_id AND th.upid = a.details->>'upid'
    WHERE a.cluster_id = c.cluster_id
      AND a.resource_type = c.resource_type
      AND (a.resource_id = c.resource_id
           OR (c.resource_type = 'ceph_osd' AND c.change = 'added'
               AND a.action = 'osd_create' AND c.node <> '' AND a.details->>'node' = c.node))
      AND COALESCE(th.started_at, a.created_at) <= c.observed_at + interval '30 seconds'
      AND CASE WHEN th.upid IS NULL THEN a.created_at ELSE COALESCE(th.finished_at, now()) END
          >= LEAST(c.last_seen_at, c.observed_at) - interval '2 minutes'
)
WHERE c.cluster_id = sqlc.arg('cluster_id')
  AND c.explained IS NULL
  AND c.observed_at < sqlc.arg('settled_before')::timestamptz
  AND (c.node = ''
       OR c.node = ANY(sqlc.arg('listed_nodes')::text[])
       OR c.observed_at < sqlc.arg('give_up_before')::timestamptz);

-- ListClusterObjectChanges is the unexplained changes of one cluster's objects of
-- the given kinds, newest first — all of a kind, or one object's when
-- resource_id is set. total is count(*) OVER the filtered set, computed before
-- LIMIT, so the page and its count come from one query.
-- name: ListClusterObjectChanges :many
SELECT c.id, c.cluster_id, c.resource_type, c.resource_id, c.vmid, c.change, c.fields, c.detail, c.node,
       c.observed_by, c.last_seen_at, c.observed_at,
       count(*) OVER ()::bigint AS total
FROM cluster_object_changes c
WHERE c.cluster_id = sqlc.arg('cluster_id')
  AND c.explained = false
  AND c.resource_type = ANY(sqlc.arg('resource_types')::text[])
  AND (sqlc.narg('resource_id')::text IS NULL OR c.resource_id = sqlc.narg('resource_id')::text)
  AND (sqlc.narg('since')::timestamptz IS NULL OR c.observed_at >= sqlc.narg('since')::timestamptz)
  AND (sqlc.narg('until')::timestamptz IS NULL OR c.observed_at < sqlc.narg('until')::timestamptz)
ORDER BY c.observed_at DESC, c.id
LIMIT sqlc.arg('row_limit')::int OFFSET sqlc.arg('row_offset')::int;
