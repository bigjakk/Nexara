-- Both insert queries derive vmid from the entry itself so app code cannot
-- forget it (the same reasoning as queries/tasks.sql). Resolution order,
-- most-authoritative first:
--
--   1. details.vmid, where the handler recorded it explicitly;
--   2. the UPID id field (field 7 of
--      UPID:<node>:<pid>:<pstart>:<starttime>:<type>:<id>:<user@realm>:),
--      which every TrackTask entry carries — but ONLY for task types whose
--      worker id is a VMID, per is_guest_upid(). That field is a plain integer
--      for other task types too: cephdestroyosd's is the OSD number, and
--      ceph_osd.go dispatches exactly that through TrackTask. Ungated, "destroy
--      OSD 113" would be stamped vmid=113 and answer a ?vmids=113 query as a
--      guest action;
--   3. resource_id, for the handlers that record the VMID there (VM create);
--   4. the vms row resource_id points at — resolved now, while the UUID is
--      still live, because that is the whole point: vms.id churns on collector
--      resync and this freezes the guest identity before it does. Only for the
--      resource types whose handlers record a vms.id (a guest's own rows and
--      its folder membership): the id of any other object is a name somebody
--      chose — a pool, an alias — and one spelled like a guest's UUID would
--      otherwise land in that guest's History. The sourced insert does it only
--      for source 'nexara': what the collector files from Proxmox is keyed by a
--      name or number Proxmox chose, never by a vms.id.
--
-- Non-guest entries (settings, logins, node actions) resolve to NULL and stay
-- NULL. {1,9} digits bounds the ::int cast, as in 000076.
-- name: InsertAuditLog :exec
INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, action, details, vmid)
VALUES ($1, $2, $3, $4, $5, $6,
    CASE
        WHEN $6::jsonb->>'vmid' ~ '^[0-9]{1,9}$'
            THEN ($6::jsonb->>'vmid')::int
        WHEN is_guest_upid($6::jsonb->>'upid')
            THEN split_part($6::jsonb->>'upid', ':', 7)::int
        WHEN $3 IN ('vm', 'container') AND $4 ~ '^[0-9]{1,9}$'
            THEN $4::int
        WHEN $3 IN ('vm', 'container', 'vm_folder_membership')
            THEN (SELECT v.vmid FROM vms v WHERE v.id::text = $4)
    END);

-- name: InsertAuditLogWithSource :exec
INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, action, details, source, created_at, vmid)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
    CASE
        WHEN $6::jsonb->>'vmid' ~ '^[0-9]{1,9}$'
            THEN ($6::jsonb->>'vmid')::int
        WHEN is_guest_upid($6::jsonb->>'upid')
            THEN split_part($6::jsonb->>'upid', ':', 7)::int
        WHEN $3 IN ('vm', 'container') AND $4 ~ '^[0-9]{1,9}$'
            THEN $4::int
        WHEN $7 = 'nexara' AND $3 IN ('vm', 'container', 'vm_folder_membership')
            THEN (SELECT v.vmid FROM vms v WHERE v.id::text = $4)
    END);

-- ListRecentAuditLogEnriched backs the dashboard activity feed. It takes the
-- caller's view:audit scope (see the accessible_cluster_ids note on
-- ListAuditLogAdvanced) rather than trimming afterwards: LIMIT 50 applied
-- before the scope would hand a cluster-scoped user whatever survives of the
-- newest 50 global rows — usually far fewer than 50, sometimes none. The
-- cluster label and the cluster_removed flag are explained there too.
--
-- The feed is the newest 50 entries plus up to 50 task entries they would miss.
-- A task writes its entry once, when it starts, so without the second arm a long
-- disk move leaves the feed while still running as soon as 50 newer entries have
-- been written. That arm takes tasks still running first, then those finished in
-- the last hour, so a long task that fails can still be read there. It leaves out
-- a "running" task on an offline node: nothing can say it is still running, and
-- the SPA polls every running row, which ties up seconds per poll on a dead node.
-- The 7 days are for a cross-cluster migration's row a hard kill left 'running':
-- ListRunningTaskHistoryByCluster withholds it from the reconciler and
-- DeleteCompletedTasks never prunes it, so it would otherwise stay in the feed
-- for good. A task_history row goes with its cluster, so this arm needs the scope
-- but not the check that the cluster exists.
-- name: ListRecentAuditLogEnriched :many
WITH feed AS (
  (SELECT a.id
   FROM audit_log a
   LEFT JOIN clusters c ON c.id = a.cluster_id
   WHERE (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
          OR a.cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
     AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL OR c.id IS NOT NULL)
   ORDER BY a.created_at DESC
   LIMIT 50)
  UNION
  (SELECT a.id
   FROM task_history th
   JOIN audit_log a ON (a.details->>'upid') = th.upid AND a.cluster_id = th.cluster_id
   WHERE th.started_at > now() - interval '7 days'
     AND ((th.status = 'running'
           AND NOT EXISTS (SELECT 1 FROM nodes n
                           WHERE n.cluster_id = th.cluster_id AND n.name = th.node AND n.status = 'offline'))
          OR th.finished_at > now() - interval '1 hour')
     AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
          OR a.cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
   ORDER BY (th.status = 'running') DESC, COALESCE(th.finished_at, th.started_at) DESC
   LIMIT 50)
)
SELECT
  a.id,
  a.cluster_id,
  a.user_id,
  a.resource_type,
  a.resource_id,
  a.action,
  a.details,
  a.created_at,
  a.source,
  u.email AS user_email,
  u.display_name AS user_display_name,
  COALESCE(c.name, rc.name, '') AS cluster_name,
  (a.cluster_id IS NOT NULL AND c.id IS NULL)::boolean AS cluster_removed,
  COALESCE(a.vmid, v.vmid, 0) AS resource_vmid,
  COALESCE(NULLIF(a.details->>'resource_name', ''), NULLIF(v.name, ''), '')::text AS resource_name,
  COALESCE(th.status, '') AS task_status,
  COALESCE(th.exit_status, '') AS task_exit_status,
  th.progress AS task_progress
FROM feed
JOIN audit_log a ON a.id = feed.id
LEFT JOIN users u ON u.id = a.user_id
LEFT JOIN clusters c ON c.id = a.cluster_id
LEFT JOIN removed_clusters rc ON rc.id = a.cluster_id
LEFT JOIN vms v ON v.id::text = a.resource_id
LEFT JOIN task_history th ON th.upid = (a.details->>'upid') AND th.cluster_id = a.cluster_id
ORDER BY a.created_at DESC;

-- ListAuditLogAdvanced backs the audit log page and the CSV/JSON/syslog export:
-- the optional cluster/type/user/action/source/time filters plus offset
-- pagination. accessible_cluster_ids carries the caller's view:audit RBAC
-- scope: NULL means global access (no restriction); an array restricts rows —
-- and the Total that CountAuditLogAdvanced feeds into pagination — to those
-- clusters ('{}' matches nothing).
--
-- audit_log.cluster_id is NULLABLE, unlike task_history's. A NULL cluster_id
-- marks a global entry (a settings change, a login) that only a holder of
-- global view:audit may read. `cluster_id = ANY(array)` yields NULL — not true
-- — for a NULL cluster_id, so this one clause already excludes those rows from
-- a scoped caller. Do NOT "repair" it into
-- `(a.cluster_id IS NULL OR a.cluster_id = ANY(...))`: that hands every global
-- entry to every cluster-scoped user. TestScopeSQL_ScopedClausesExcludeNullCluster
-- pins the shape, and TestAuditScope_NullClusterRowsAreGlobal the behaviour.
--
-- An entry outlives the cluster it was recorded against (000122 dropped the
-- foreign key): it keeps its cluster_id, which then names no clusters row. The
-- cluster's name was kept in removed_clusters by a trigger on its removal, and
-- cluster_name falls back to it, with cluster_removed set; it is empty only for an
-- entry with no cluster, or whose cluster's name was not kept, and saying "removed"
-- is the reader's to write. Such an entry stays readable by a holder of GLOBAL
-- view:audit only, as one with no cluster is. A role assignment scoped to a cluster
-- is deleted with it (000123), so none names one that is gone; the second
-- accessible_cluster_ids conjunct requires the cluster to exist all the same, because
-- a cached permission set (the cache lives five minutes) still holds the grant after
-- its row is gone, and its id would otherwise match `cluster_id = ANY(array)`. The
-- conjunct is kept apart from the pinned clause above, which it must not change,
-- and TestAuditScope_NullClusterRowsAreGlobal holds it.
--
-- vmids filters on the denormalized audit_log.vmid (000084), mirroring the
-- same narg on ListTaskHistoryAdvanced. It deliberately reads the column and
-- not the vms join: the column is the guest identity frozen at insert, so the
-- filter keeps working for destroyed guests and across the collector's vms.id
-- churn — which is the whole reason the column exists.
-- name: ListAuditLogAdvanced :many
SELECT
  a.id,
  a.cluster_id,
  a.user_id,
  a.resource_type,
  a.resource_id,
  a.action,
  a.details,
  a.created_at,
  a.source,
  u.email AS user_email,
  u.display_name AS user_display_name,
  COALESCE(c.name, rc.name, '') AS cluster_name,
  (a.cluster_id IS NOT NULL AND c.id IS NULL)::boolean AS cluster_removed,
  COALESCE(a.vmid, v.vmid, 0) AS resource_vmid,
  COALESCE(NULLIF(a.details->>'resource_name', ''), NULLIF(v.name, ''), '')::text AS resource_name
FROM audit_log a
LEFT JOIN users u ON u.id = a.user_id
LEFT JOIN clusters c ON c.id = a.cluster_id
LEFT JOIN removed_clusters rc ON rc.id = a.cluster_id
LEFT JOIN vms v ON v.id::text = a.resource_id
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR a.cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('resource_type')::text IS NULL OR a.resource_type = sqlc.narg('resource_type'))
  AND (sqlc.narg('user_id')::uuid IS NULL OR a.user_id = sqlc.narg('user_id'))
  AND (sqlc.narg('action')::text IS NULL OR a.action = sqlc.narg('action'))
  AND (sqlc.narg('source')::text IS NULL OR a.source = sqlc.narg('source'))
  AND (sqlc.narg('start_time')::timestamptz IS NULL OR a.created_at >= sqlc.narg('start_time'))
  AND (sqlc.narg('end_time')::timestamptz IS NULL OR a.created_at <= sqlc.narg('end_time'))
  AND (sqlc.narg('vmids')::int[] IS NULL OR a.vmid = ANY(sqlc.narg('vmids')::int[]))
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR a.cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL OR c.id IS NOT NULL)
ORDER BY a.created_at DESC
LIMIT $1 OFFSET $2;

-- CountAuditLogAdvanced returns the total matching the same filters, for the
-- audit page pagination. Must stay filter-for-filter in sync with
-- ListAuditLogAdvanced — in particular accessible_cluster_ids, with the
-- existence conjunct beside it, or the Total leaks how many entries the
-- caller's inaccessible clusters hold.
-- name: CountAuditLogAdvanced :one
SELECT count(*) FROM audit_log
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('resource_type')::text IS NULL OR resource_type = sqlc.narg('resource_type'))
  AND (sqlc.narg('user_id')::uuid IS NULL OR user_id = sqlc.narg('user_id'))
  AND (sqlc.narg('action')::text IS NULL OR action = sqlc.narg('action'))
  AND (sqlc.narg('source')::text IS NULL OR source = sqlc.narg('source'))
  AND (sqlc.narg('start_time')::timestamptz IS NULL OR created_at >= sqlc.narg('start_time'))
  AND (sqlc.narg('end_time')::timestamptz IS NULL OR created_at <= sqlc.narg('end_time'))
  AND (sqlc.narg('vmids')::int[] IS NULL OR vmid = ANY(sqlc.narg('vmids')::int[]))
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR EXISTS (SELECT 1 FROM clusters c WHERE c.id = audit_log.cluster_id));

-- name: ListDistinctAuditActions :many
SELECT DISTINCT action FROM audit_log ORDER BY action;

-- name: ListDistinctAuditUsers :many
SELECT DISTINCT u.id, u.email, u.display_name
FROM audit_log a
JOIN users u ON u.id = a.user_id
ORDER BY u.display_name;
