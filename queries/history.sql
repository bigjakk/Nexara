-- The History timeline every kind of object shares: one page of an object's
-- entries, newest first, with the total under the same filters. Served by the
-- History routes (internal/api/registry_history.go), each a kind of object
-- whose handlers.HistoryKind says which rows are its own and how they are
-- categorized, from these stores:
--
--   audit_log            every action Nexara recorded — its own tasks,
--                        Proxmox's (ingested by the collector), DRS, the
--                        scheduler, config changes with no task. Kept forever.
--   task_history (+ the  how each task ended. task_history_archive holds the
--   _archive)            guest tasks task_history has pruned (000115).
--   guest_status_changes run-state changes no task explains (000115).
--   guest_backups        a guest's part of each backup JOB — a vzdump of
--                        several guests, whose task names none — read from the
--                        job's log by the collector (000116).
--
-- A kind names its rows through two families of selector, either or both:
--
--   guests   (vmids, each with its own cutoff): the guest-shaped sources. A
--            guest is (cluster_id, vmid), never a vms.id: the collector
--            re-issues those (000068/000084), and a VMID is only unique within
--            its cluster — the other cluster's VM 100 is a different guest.
--            guest_workers, when set, keeps only the guests' rows whose UPID
--            names a worker type LIKE one of them (an HA resource's History
--            holds its guest's ha* tasks and nothing else of the guest's).
--   objects  (parallel object_* arrays): the generic selector — the audit rows
--            of one resource type that are one object's:
--              no key       resource_id is the value;
--              a key        details->>key is the value, a string or an
--                           integer, within one resource_id when one is named
--                           (a PBS datastore within its server's rows);
--              a list key   the value is one element of a comma-separated
--                           details key (an ACL's users). Read by resource
--                           type alone, so for a type with few rows.
--
-- Every branch reads one cluster, a REQUIRED argument: the route's, which its
-- cluster-scoped gate authorized. The one exception is a global kind's objects
-- (global, HistoryKind.Global): read in every cluster and in none, because
-- their ids are unique across Nexara — a PBS server's, Veeam's, a firewall
-- template's — and their route's gate is all GLOBAL. A global kind has no
-- guests. Each entry carries its own cluster_id, which its task outcome is
-- joined on.

-- ListHistory is the timeline.
--
-- Entries, one per thing that happened:
--
--   audit    an audit row the selectors name. For a guest: by vmid (000084); a
--            clone INTO it, which names its source (see
--            ListGuestLifecycleEvents); or an HA resource row, which names the
--            guest only by its HA sid ("vm:100", "ct:100") and so carries no
--            vmid. For an object: by its resource identity. A Proxmox-ingested
--            row is dropped when a Nexara row of the selection carries the same
--            UPID: the collector's dedupe can lose a race with a handler's own
--            insert, and the two rows are one task, the Nexara one the one that
--            says who asked. A twin outside the selection drops nothing — a
--            node's History keeps a guest task it selects by details.node even
--            when the Nexara row of it, filed under the guest, is not its own.
--   task     a guest task with NO audit row, from task_history or its archive
--            — only when include_tasks, which the handler sets from view:task.
--            Every task path writes an audit row too, so this is the rare row
--            whose audit insert failed; sharing a UPID with an audit row makes
--            a task the same entry, never a second one.
--   status   a status change no task explains (ClassifyGuestStatusChanges),
--            for the guest types in status_guest_types — the run state is
--            inventory data, which the handler reads under view:vm for a VM and
--            view:container for a container.
--   backup   a guest's part of a backup JOB (guest_backups), with the guest's
--            own outcome and times rather than the job's — only when
--            include_tasks, since all of it is read from the job's task log.
--            A job's own audit row names no guest, so it is never a second
--            entry; nor is its task row, whose vmid is NULL.
--
-- Guest tasks only: is_guest_upid, never vmid alone, because InsertTaskHistory
-- stamps a vmid on any numeric worker id — cephdestroyosd's is an OSD number,
-- so "destroy OSD 113" would otherwise read as something done to VM 113.
-- audit_log.vmid is already gated the same way at insert.
--
-- The audit branches are four rather than one OR, so each reads its own index
-- — the vmid arm (cluster_id, vmid), the clone arm the action index, the HA arm
-- the resource_type index, the object arm 000117's — instead of the planner
-- settling on the whole cluster's rows. Each later arm excludes the rows an
-- earlier one keeps, so no row is read twice; a guest's row guest_workers turns
-- away is still an object's to take. The object arm reads each object on its
-- own: (resource_type, resource_id) for its own rows, GIN details @> {key:
-- value} for a details key — a filter on details->>key read every row of the
-- type. "vmid IS NOT NULL" lets the vmid arms use their partial indexes
-- whatever the plan: a generic plan cannot prove it from "= ANY(<parameter>)".
-- The Proxmox-duplicate probe reads the selection
-- itself, never audit_log: probed there by UPID, a generic plan — pgx's
-- statement cache lets Postgres switch to one after five executions — scans
-- the whole cluster's rows on every request.
--
-- subject is the guest an entry is filed under — the clone's target, the HA
-- sid's guest — and picks the cutoff it is held to: cutoff_after and
-- cutoff_inclusive, parallel to vmids, from guestHistoryCutoff. Inclusive from
-- a birth, so the create or clone is the first entry; exclusive after a
-- destroy, so the previous guest's last act is not this one's first. The zero
-- time is no cutoff. An object's row has no subject and no cutoff, unless it
-- names one of the guests — a row of the guest's that guest_workers turned
-- away and an object took — which is still that guest's and held to its
-- cutoff: a previous holder of the VMID is not the object's either.
--
-- since, and cutoff_floor — the earliest of the guests' cutoffs, which no
-- guest's row comes before; NULL when a guest has none — are repeated in every
-- arm: entries is read twice, so it is materialized, and a bound applied to it
-- reaches no arm. A 24-hour window on the busiest guest on dev read all its
-- rows otherwise. Both are written "IS NULL OR": a bare range on a parameter
-- tempts a generic plan to AND the arm's index with the created_at index,
-- which read all of dev's 32k audit rows for a 30-row guest. Each entry finds
-- its guest's cutoff by joining guests on its subject, materialized so the
-- plan does not rebuild the list for every row.
--
-- category is derived HERE, once, so it can be filtered on before the page is
-- cut. The rules are the kind's (HistoryKind.Rules), passed in order as
-- parallel arrays: an entry takes the category of the first rule whose field
-- is LIKE its pattern, else default_category. Only four fields decide it, so
-- category_map works it out once per distinct (entry kind, action, resource
-- type, UPID worker type) the selection holds — a few dozen, against
-- thousands of rows — and each row looks its own up. Rules matched per row
-- cost four times as much on the busiest guest on dev. The map's key is
-- unambiguous: the entry kind is a constant and the worker type a UPID field,
-- so neither holds a ':', and the resource type is length-prefixed, which
-- leaves the action, last, to be the rest.
--
-- The page is cut on narrow rows, and only its rows are joined to their audit
-- details, task outcome and user: a guest with ten thousand entries carries
-- ten thousand jsonb blobs through the sort otherwise. A backup entry's
-- outcome, end and log lines are the guest's own, from guest_backups; the
-- job's own task row, joined like any other, gives the rest. total is
-- count(*) OVER the filtered set, computed before LIMIT — the Items and the
-- Total come from one query and so cannot be counted under different filters.
-- name: ListHistory :many
WITH guests AS MATERIALIZED (
    SELECT v.vmid, a.after, i.inclusive
    FROM unnest(sqlc.arg('vmids')::int[]) WITH ORDINALITY AS v(vmid, n)
    JOIN unnest(sqlc.arg('cutoff_after')::timestamptz[]) WITH ORDINALITY AS a(after, n) ON a.n = v.n
    JOIN unnest(sqlc.arg('cutoff_inclusive')::bool[]) WITH ORDINALITY AS i(inclusive, n) ON i.n = v.n
), objects AS (
    SELECT t.resource_type, r.resource_id, k.detail_key, v.value, l.in_list
    FROM unnest(sqlc.arg('object_types')::text[]) WITH ORDINALITY AS t(resource_type, n)
    JOIN unnest(sqlc.arg('object_resource_ids')::text[]) WITH ORDINALITY AS r(resource_id, n) ON r.n = t.n
    JOIN unnest(sqlc.arg('object_keys')::text[]) WITH ORDINALITY AS k(detail_key, n) ON k.n = t.n
    JOIN unnest(sqlc.arg('object_values')::text[]) WITH ORDINALITY AS v(value, n) ON v.n = t.n
    JOIN unnest(sqlc.arg('object_in_list')::bool[]) WITH ORDINALITY AS l(in_list, n) ON l.n = t.n
), rules AS (
    SELECT c.category, f.field, p.pattern, c.n AS ord
    FROM unnest(sqlc.arg('rule_categories')::text[]) WITH ORDINALITY AS c(category, n)
    JOIN unnest(sqlc.arg('rule_fields')::text[]) WITH ORDINALITY AS f(field, n) ON f.n = c.n
    JOIN unnest(sqlc.arg('rule_patterns')::text[]) WITH ORDINALITY AS p(pattern, n) ON p.n = c.n
), audit AS (
    SELECT a.id, a.cluster_id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid, a.user_id,
           a.details->>'upid' AS upid, a.details->>'node' AS node, a.details->>'status' AS recorded_status,
           a.vmid AS subject
    FROM audit_log a
    WHERE a.cluster_id = sqlc.arg('cluster_id')::uuid
      AND a.vmid IS NOT NULL
      AND a.vmid = ANY(sqlc.arg('vmids')::int[])
      AND history_worker_ok(a.details->>'upid', sqlc.arg('guest_workers')::text[])
      AND (sqlc.narg('cutoff_floor')::timestamptz IS NULL OR a.created_at >= sqlc.narg('cutoff_floor')::timestamptz)
      AND (sqlc.narg('since')::timestamptz IS NULL OR a.created_at >= sqlc.narg('since')::timestamptz)
    UNION ALL
    SELECT a.id, a.cluster_id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid, a.user_id,
           a.details->>'upid', a.details->>'node', a.details->>'status',
           (a.details->>'new_id')::int
    FROM audit_log a
    WHERE a.action IN ('clone', 'clone-to-template')
      AND a.cluster_id = sqlc.arg('cluster_id')::uuid
      AND NOT COALESCE(a.vmid = ANY(sqlc.arg('vmids')::int[]), false)
      AND a.details->>'new_id' = ANY(sqlc.arg('vmids')::int[]::text[])
      AND history_worker_ok(a.details->>'upid', sqlc.arg('guest_workers')::text[])
      AND (sqlc.narg('cutoff_floor')::timestamptz IS NULL OR a.created_at >= sqlc.narg('cutoff_floor')::timestamptz)
      AND (sqlc.narg('since')::timestamptz IS NULL OR a.created_at >= sqlc.narg('since')::timestamptz)
    UNION ALL
    SELECT a.id, a.cluster_id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid, a.user_id,
           a.details->>'upid', a.details->>'node', a.details->>'status',
           split_part(a.resource_id, ':', 2)::int
    FROM audit_log a
    WHERE a.resource_type = 'ha_resource'
      AND a.cluster_id = sqlc.arg('cluster_id')::uuid
      AND NOT COALESCE(a.vmid = ANY(sqlc.arg('vmids')::int[]), false)
      AND a.resource_id = ANY(ARRAY(SELECT p || v FROM unnest(sqlc.arg('vmids')::int[]) AS v,
                                                     unnest('{vm:,ct:}'::text[]) AS p))
      AND history_worker_ok(a.details->>'upid', sqlc.arg('guest_workers')::text[])
      AND (sqlc.narg('cutoff_floor')::timestamptz IS NULL OR a.created_at >= sqlc.narg('cutoff_floor')::timestamptz)
      AND (sqlc.narg('since')::timestamptz IS NULL OR a.created_at >= sqlc.narg('since')::timestamptz)
    UNION ALL
    SELECT a.id, a.cluster_id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid, a.user_id,
           a.details->>'upid', a.details->>'node', a.details->>'status',
           CASE WHEN a.vmid = ANY(sqlc.arg('vmids')::int[]) THEN a.vmid
                WHEN a.action IN ('clone', 'clone-to-template')
                     AND a.details->>'new_id' = ANY(sqlc.arg('vmids')::int[]::text[])
                    THEN (a.details->>'new_id')::int
                WHEN a.resource_type = 'ha_resource'
                     AND a.resource_id = ANY(ARRAY(SELECT p || v FROM unnest(sqlc.arg('vmids')::int[]) AS v,
                                                                    unnest('{vm:,ct:}'::text[]) AS p))
                    THEN split_part(a.resource_id, ':', 2)::int
           END
    FROM audit_log a
    WHERE cardinality(sqlc.arg('object_types')::text[]) > 0
      AND a.id IN (
          SELECT m.id
          FROM objects o
          CROSS JOIN LATERAL (
              SELECT b.id FROM audit_log b
              WHERE o.detail_key = ''
                AND b.resource_type = o.resource_type AND b.resource_id = o.value
                AND (sqlc.arg('global')::bool OR b.cluster_id = sqlc.arg('cluster_id')::uuid)
              UNION ALL
              SELECT b.id FROM audit_log b
              WHERE o.detail_key <> '' AND NOT o.in_list
                AND (b.details @> jsonb_build_object(o.detail_key, o.value)
                     OR b.details @> CASE WHEN o.value ~ '^-?[0-9]{1,15}$'
                                          THEN jsonb_build_object(o.detail_key, o.value::bigint) END)
                AND b.details->>o.detail_key = o.value
                AND b.resource_type = o.resource_type
                AND (o.resource_id = '' OR b.resource_id = o.resource_id)
                AND (sqlc.arg('global')::bool OR b.cluster_id = sqlc.arg('cluster_id')::uuid)
              UNION ALL
              SELECT b.id FROM audit_log b
              WHERE o.in_list
                AND b.resource_type = o.resource_type
                AND (o.resource_id = '' OR b.resource_id = o.resource_id)
                AND o.value = ANY(regexp_split_to_array(btrim(b.details->>o.detail_key), '\s*,\s*'))
                AND (sqlc.arg('global')::bool OR b.cluster_id = sqlc.arg('cluster_id')::uuid)
          ) m)
      AND NOT COALESCE(a.vmid = ANY(sqlc.arg('vmids')::int[])
                       AND history_worker_ok(a.details->>'upid', sqlc.arg('guest_workers')::text[]), false)
      AND NOT COALESCE(a.action IN ('clone', 'clone-to-template')
                       AND a.details->>'new_id' = ANY(sqlc.arg('vmids')::int[]::text[])
                       AND history_worker_ok(a.details->>'upid', sqlc.arg('guest_workers')::text[]), false)
      AND NOT COALESCE(a.resource_type = 'ha_resource'
                       AND a.resource_id = ANY(ARRAY(SELECT p || v FROM unnest(sqlc.arg('vmids')::int[]) AS v,
                                                                      unnest('{vm:,ct:}'::text[]) AS p))
                       AND history_worker_ok(a.details->>'upid', sqlc.arg('guest_workers')::text[]), false)
      AND (sqlc.narg('since')::timestamptz IS NULL OR a.created_at >= sqlc.narg('since')::timestamptz)
), entries AS (
    SELECT ('audit:' || s.id::text)::text           AS entry_id,
           'audit'::text                            AS kind,
           s.id                                     AS ref_id,
           s.cluster_id                             AS cluster_id,
           s.created_at                             AS occurred_at,
           s.source                                 AS source,
           s.action                                 AS action,
           s.resource_type                          AS resource_type,
           s.resource_id                            AS resource_id,
           COALESCE(s.vmid, s.subject, 0)::int      AS vmid,
           s.subject                                AS subject,
           s.user_id                                AS user_id,
           COALESCE(s.upid, '')::text               AS upid,
           COALESCE(s.node, '')::text               AS node,
           COALESCE(s.recorded_status, '')::text    AS recorded_status,
           ''::text                                 AS from_status,
           ''::text                                 AS to_status,
           NULL::timestamptz                        AS last_seen_at
    FROM audit s
    WHERE NOT (s.source = 'proxmox' AND s.upid IS NOT NULL AND EXISTS (
            SELECT 1 FROM audit b WHERE b.source <> 'proxmox' AND b.upid = s.upid))
    UNION ALL
    SELECT ('task:' || t.upid)::text, 'task', NULL::uuid, t.cluster_id, t.started_at, t.source, t.task_type,
           'task', '', t.vmid, t.vmid, t.user_id, t.upid, t.node, '', '', '', NULL
    FROM task_history t
    WHERE sqlc.arg('include_tasks')::bool
      AND t.cluster_id = sqlc.arg('cluster_id')::uuid
      AND t.vmid IS NOT NULL
      AND t.vmid = ANY(sqlc.arg('vmids')::int[])
      AND is_guest_upid(t.upid)
      AND history_worker_ok(t.upid, sqlc.arg('guest_workers')::text[])
      AND (sqlc.narg('cutoff_floor')::timestamptz IS NULL OR t.started_at >= sqlc.narg('cutoff_floor')::timestamptz)
      AND (sqlc.narg('since')::timestamptz IS NULL OR t.started_at >= sqlc.narg('since')::timestamptz)
      AND NOT EXISTS (SELECT 1 FROM audit_log a
                      WHERE a.details->>'upid' = t.upid AND a.cluster_id = t.cluster_id)
    UNION ALL
    SELECT ('task:' || r.upid)::text, 'task', NULL::uuid, r.cluster_id, r.started_at, r.source, r.task_type,
           'task', '', r.vmid, r.vmid, r.user_id, r.upid, r.node, '', '', '', NULL
    FROM task_history_archive r
    WHERE sqlc.arg('include_tasks')::bool
      AND r.cluster_id = sqlc.arg('cluster_id')::uuid
      AND r.vmid = ANY(sqlc.arg('vmids')::int[])
      AND history_worker_ok(r.upid, sqlc.arg('guest_workers')::text[])
      AND (sqlc.narg('cutoff_floor')::timestamptz IS NULL OR r.started_at >= sqlc.narg('cutoff_floor')::timestamptz)
      AND (sqlc.narg('since')::timestamptz IS NULL OR r.started_at >= sqlc.narg('since')::timestamptz)
      AND NOT EXISTS (SELECT 1 FROM audit_log a
                      WHERE a.details->>'upid' = r.upid AND a.cluster_id = r.cluster_id)
    UNION ALL
    SELECT ('status:' || s.id::text)::text, 'status', s.id, s.cluster_id, s.observed_at, 'detected', 'status_change',
           'status', '', s.vmid, s.vmid, NULL::uuid, '', s.node, '', s.from_status, s.to_status, s.last_seen_at
    FROM guest_status_changes s
    WHERE s.cluster_id = sqlc.arg('cluster_id')::uuid
      AND s.vmid = ANY(sqlc.arg('vmids')::int[])
      AND s.explained = false
      AND s.guest_type = ANY(sqlc.arg('status_guest_types')::text[])
      AND history_worker_ok(NULL, sqlc.arg('guest_workers')::text[])
      AND (sqlc.narg('cutoff_floor')::timestamptz IS NULL OR s.observed_at >= sqlc.narg('cutoff_floor')::timestamptz)
      AND (sqlc.narg('since')::timestamptz IS NULL OR s.observed_at >= sqlc.narg('since')::timestamptz)
    UNION ALL
    SELECT ('backup:' || b.upid || ':' || b.vmid::text)::text, 'backup', NULL::uuid, b.cluster_id, b.started_at, b.source, 'vzdump',
           'backup', '', b.vmid, b.vmid, b.user_id, b.upid, b.node, '', '', '', NULL
    FROM guest_backups b
    WHERE sqlc.arg('include_tasks')::bool
      AND b.cluster_id = sqlc.arg('cluster_id')::uuid
      AND b.vmid = ANY(sqlc.arg('vmids')::int[])
      AND history_worker_ok(b.upid, sqlc.arg('guest_workers')::text[])
      AND (sqlc.narg('cutoff_floor')::timestamptz IS NULL OR b.started_at >= sqlc.narg('cutoff_floor')::timestamptz)
      AND (sqlc.narg('since')::timestamptz IS NULL OR b.started_at >= sqlc.narg('since')::timestamptz)
), category_map AS MATERIALIZED (
    SELECT jsonb_object_agg(
               k.kind || ':' || k.worker || ':' || length(k.resource_type) || ':' || k.resource_type
                   || ':' || k.action,
               COALESCE((SELECT r.category
                         FROM rules r
                         WHERE CASE r.field
                                   WHEN 'entry' THEN k.kind
                                   WHEN 'action' THEN k.action
                                   WHEN 'resource_type' THEN k.resource_type
                                   WHEN 'worker' THEN k.worker
                               END LIKE r.pattern
                         ORDER BY r.ord
                         LIMIT 1), sqlc.arg('default_category')::text)) AS categories
    FROM (SELECT DISTINCT e.kind, e.action, e.resource_type, split_part(e.upid, ':', 6) AS worker
          FROM entries e) k
), categorized AS (
    SELECT e.*,
           ((SELECT m.categories FROM category_map m)
               ->> (e.kind || ':' || split_part(e.upid, ':', 6) || ':' || length(e.resource_type) || ':'
                    || e.resource_type || ':' || e.action))::text AS category
    FROM entries e
), page AS (
    SELECT c.*, count(*) OVER () AS total
    FROM categorized c
    LEFT JOIN guests g ON g.vmid = c.subject
    WHERE (g.vmid IS NULL OR c.occurred_at > g.after OR (g.inclusive AND c.occurred_at = g.after))
      AND (sqlc.narg('since')::timestamptz IS NULL OR c.occurred_at >= sqlc.narg('since')::timestamptz)
      AND (sqlc.narg('category')::text IS NULL OR c.category = sqlc.narg('category')::text)
      AND (sqlc.narg('source')::text IS NULL OR c.source = sqlc.narg('source')::text)
    ORDER BY c.occurred_at DESC, c.entry_id DESC
    LIMIT sqlc.arg('row_limit')::int OFFSET sqlc.arg('row_offset')::int
)
SELECT p.entry_id, p.kind, p.cluster_id, p.occurred_at, p.source, p.action, p.resource_type, p.resource_id, p.vmid,
       COALESCE(p.subject, 0)::int AS subject_vmid,
       p.user_id, p.upid, p.node, p.recorded_status, p.from_status, p.to_status, p.last_seen_at,
       p.category, p.total,
       COALESCE(ad.details, '{}'::jsonb)                      AS details,
       COALESCE(u.email, '')::text                            AS user_email,
       COALESCE(u.display_name, '')::text                     AS user_display_name,
       COALESCE(gb.status, th.status, ar.status, '')::text               AS task_status,
       COALESCE(gb.exit_status, th.exit_status, ar.exit_status, '')::text AS task_exit_status,
       COALESCE(th.description, ar.description, '')::text                AS task_description,
       COALESCE(th.task_type, ar.task_type, '')::text                    AS task_type,
       COALESCE(th.node, ar.node, '')::text                              AS task_node,
       th.progress                                                       AS task_progress,
       COALESCE(th.finished_at, ar.finished_at)                          AS task_finished_at,
       gb.finished_at                                                    AS backup_finished_at,
       COALESCE(gb.log_start, 0)::int                                    AS log_start,
       COALESCE(gb.log_end, 0)::int                                      AS log_end
FROM page p
LEFT JOIN audit_log ad
       ON p.kind = 'audit' AND ad.id = p.ref_id
LEFT JOIN task_history th
       ON p.upid <> '' AND th.upid = p.upid AND th.cluster_id = p.cluster_id
LEFT JOIN task_history_archive ar
       ON p.upid <> '' AND ar.upid = p.upid AND ar.cluster_id = p.cluster_id
LEFT JOIN guest_backups gb
       ON p.kind = 'backup' AND gb.upid = p.upid AND gb.vmid = p.vmid
          AND gb.cluster_id = p.cluster_id
LEFT JOIN users u ON u.id = p.user_id
ORDER BY p.occurred_at DESC, p.entry_id DESC;

-- ListGuestTypes is the type of each of a History's guests the inventory
-- holds, for the handler to keep only the guests the caller may open — a VM
-- with view:vm, a container with view:container — when the guests are the
-- kind's own choice rather than the one the path names (viewableGuests).
-- name: ListGuestTypes :many
SELECT vmid, type
FROM vms
WHERE cluster_id = sqlc.arg('cluster_id')
  AND vmid = ANY(sqlc.arg('vmids')::int[]);
