-- A guest's History tab: everything that happened to one guest, newest first.
-- Served by GET /api/v1/clusters/:cluster_id/guests/:vmid/history
-- (internal/api/handlers/guest_history.go), from three stores:
--
--   audit_log            every action Nexara recorded about the guest — its own
--                        tasks, Proxmox's (ingested by the collector), DRS, the
--                        scheduler, config changes with no task. Kept forever.
--   task_history (+ the  how each task ended. task_history_archive holds the
--   _archive)            guest tasks task_history has pruned (000115).
--   guest_status_changes run-state changes no task explains (000115).
--
-- A guest is (cluster_id, vmid), never a vms.id: the collector re-issues those
-- (000068/000084), and a VMID is only unique within its cluster — the other
-- cluster's VM 100 is a different guest.

-- ClassifyGuestStatusChanges decides, once, whether a task explains each
-- settled status change on a cluster: one that started (allowing 30s of clock
-- skew between Nexara and the node) before the change was seen, and had not
-- ended before the guest was last seen in its old state, less 2 minutes for
-- how stale that sighting can be (pvestatd's 10s cycle, then the fast sync's
-- interval). last_seen_at rather than observed_at is what keeps a gap honest:
-- after Nexara or the node was unreachable, a change is first seen when it is
-- reachable again, long after the task that made it.
--
-- "Settled" is the caller's settled_before: a change seen more recently stays
-- unclassified, and hidden, until the collector has had time to ingest the
-- Proxmox task that may explain it. A stop from the Proxmox GUI reaches the
-- inventory before its qmstop task reaches task_history; classifying it at once
-- would show it as a stop no task explains, and then never take that back.
--
-- Which task types explain which change is the direction-specific list below,
-- because a task is evidence only for what it can do: an HA restart that begins
-- seconds after a crash does not explain the crash. The "any" set changes a
-- guest's state in either direction as part of its work (a reboot, a restore,
-- a backup in stop or suspend mode or of a stopped VM, which runs it in
-- prelaunch). An unknown type explains nothing — the change stays visible,
-- which is the side to err on for a list whose point is to show the stops a
-- task did not cause. A suspend is in the stopped list too: a hibernating VM
-- stops, and a container's suspend checkpoints it and stops it, after the task
-- watcher's predicted "suspended".
--
-- The last arm is a backup JOB: a vzdump that backs up several guests carries
-- no VMID (UPID worker id empty), so it explains the changes on the node it
-- ran on while it ran. A real crash of another guest on that node during the
-- backup window is hidden by it; that is the trade.
--
-- Only the changes on listed_nodes — the nodes whose tasks this pass's ingest
-- listed (syncTasks) — are decided; one whose node could not list waits, and
-- may then wait past TASK_HISTORY_RETENTION, which is why the search covers
-- task_history_archive too. On an offline_nodes member only a change to or
-- from 'unknown' is decided — no task explains one — while its others wait
-- for the tasks it has not handed over (a node rebooting shuts its guests down
-- just before it goes). The archive holds guest tasks only, so a backup JOB
-- (below) explains nothing once pruned. A change older than give_up_before
-- is decided whatever its node: a node the cluster calls online whose task list
-- never loads would otherwise hide its guests' changes for good, and deciding
-- without its tasks errs on the side this list exists for — the change shows.
-- Once made, the decision is stored and does not depend on the task rows
-- surviving.
--
-- The archive branch is bounded to the guest and to tasks that finished after
-- the window opened, which (cluster_id, vmid, finished_at) serves: everything
-- in the archive is older than the retention, so a recent change reads none
-- of it.
-- name: ClassifyGuestStatusChanges :execrows
UPDATE guest_status_changes sc
SET explained = EXISTS (
    SELECT 1
    FROM (SELECT th.cluster_id, th.vmid, th.upid, th.node, th.started_at, th.finished_at
          FROM task_history th
          UNION ALL
          SELECT ar.cluster_id, ar.vmid, ar.upid, ar.node, ar.started_at, ar.finished_at
          FROM task_history_archive ar
          WHERE ar.cluster_id = sc.cluster_id AND ar.vmid = sc.vmid
            AND ar.finished_at >= LEAST(sc.last_seen_at, sc.observed_at) - interval '2 minutes') t
    WHERE t.cluster_id = sc.cluster_id
      AND t.started_at <= sc.observed_at + interval '30 seconds'
      AND COALESCE(t.finished_at, now()) >= LEAST(sc.last_seen_at, sc.observed_at) - interval '2 minutes'
      AND (
            (t.vmid = sc.vmid AND is_guest_upid(t.upid) AND (
                    split_part(t.upid, ':', 6) LIKE 'ha%'
                 OR split_part(t.upid, ':', 6) IN ('qmreboot', 'vzreboot', 'qmreset', 'qmrollback',
                                                   'vzrollback', 'qmrestore', 'vzrestore', 'qmigrate',
                                                   'vzmigrate', 'vzdump', 'qmsnapshot', 'vzsnapshot')
                 OR (sc.to_status = 'running'
                     AND split_part(t.upid, ':', 6) IN ('qmstart', 'vzstart', 'qmresume', 'vzresume'))
                 OR (sc.to_status = 'stopped'
                     AND split_part(t.upid, ':', 6) IN ('qmstop', 'qmshutdown', 'vzstop', 'vzshutdown',
                                                       'qmdestroy', 'vzdestroy', 'qmsuspend', 'vzsuspend'))
                 OR (sc.to_status NOT IN ('running', 'stopped')
                     AND split_part(t.upid, ':', 6) IN ('qmsuspend', 'qmpause', 'vzsuspend'))
            ))
         OR (t.vmid IS NULL AND split_part(t.upid, ':', 6) = 'vzdump' AND t.node = sc.node)
      )
)
WHERE sc.cluster_id = sqlc.arg('cluster_id')
  AND sc.explained IS NULL
  AND sc.observed_at < sqlc.arg('settled_before')::timestamptz
  AND (sc.node = ANY(sqlc.arg('listed_nodes')::text[])
       OR (sc.node = ANY(sqlc.arg('offline_nodes')::text[]) AND 'unknown' IN (sc.from_status, sc.to_status))
       OR sc.observed_at < sqlc.arg('give_up_before')::timestamptz);

-- ListGuestLifecycleEvents returns the audit rows that begin or end a guest's
-- occupancy of a VMID, newest first, for the handler to pick where the
-- timeline starts (guestHistoryCutoff). Proxmox hands out the lowest free
-- VMID, so VM 105 today may be the third guest to hold that number, and
-- without a cutoff the first two guests' history would read as this one's.
--
-- A birth is a create or restore of the VMID — by the UPID's worker type,
-- which both Nexara's handlers and the collector's ingest record — or a clone
-- INTO it. A clone's UPID names its SOURCE, so the target is read from the
-- new_id the clone handlers record; a clone made in Proxmox's own GUI records
-- no target and is no birth here, which leaves the previous occupant's destroy
-- as the cutoff instead.
--
-- A destroy comes back with its outcome, because a FAILED destroy ended
-- nothing; the handler decides with proxmox.TaskSucceeded rather than a SQL
-- copy of that rule. recorded_status is the status the collector stored when
-- it ingested a task, for a task whose row has since been pruned.
--
-- Bounded: the handler only needs the newest birth and the newest destroy that
-- did not fail, and a guest restored nightly would otherwise drag in a year of
-- restores.
-- name: ListGuestLifecycleEvents :many
SELECT a.created_at,
       COALESCE(split_part(a.details->>'upid', ':', 6) IN ('qmdestroy', 'vzdestroy'), false)::bool AS is_destroy,
       COALESCE(th.status, ar.status, '')::text                                   AS task_status,
       COALESCE(th.exit_status, ar.exit_status, '')::text                         AS task_exit_status,
       COALESCE(a.details->>'status', '')::text                                   AS recorded_status
FROM audit_log a
LEFT JOIN task_history th
       ON th.upid = a.details->>'upid' AND th.cluster_id = a.cluster_id
LEFT JOIN task_history_archive ar
       ON ar.upid = a.details->>'upid' AND ar.cluster_id = a.cluster_id
WHERE a.cluster_id = sqlc.arg('cluster_id')
  AND (
        (a.vmid = sqlc.arg('vmid')::int
         AND split_part(a.details->>'upid', ':', 6) IN ('qmcreate', 'vzcreate', 'qmrestore',
                                                        'vzrestore', 'qmdestroy', 'vzdestroy'))
     OR (a.action IN ('clone', 'clone-to-template')
         AND a.details->>'new_id' = sqlc.arg('vmid')::int::text)
  )
ORDER BY a.created_at DESC, a.id
LIMIT 50;

-- ListGuestHistory is the timeline: one page of a guest's entries, newest
-- first, with the total under the same filters.
--
-- Entries, one per thing that happened:
--
--   audit    an audit row naming the guest: by vmid (000084); a clone INTO it,
--            which names its source (see ListGuestLifecycleEvents); or an HA
--            resource row, which names the guest only by its HA sid ("vm:100",
--            "ct:100") and so carries no vmid. A Proxmox-ingested row is
--            dropped when a Nexara row carries the same UPID: the collector's
--            dedupe can lose a race with a handler's own insert, and the two
--            rows are one task, the Nexara one the one that says who asked.
--   task     a guest task with NO audit row, from task_history or its archive
--            — only when include_tasks, which the handler sets from view:task.
--            Every task path writes an audit row too, so this is the rare row
--            whose audit insert failed; sharing a UPID with an audit row makes
--            a task the same entry, never a second one.
--   status   a status change no task explains (ClassifyGuestStatusChanges),
--            for the guest types in status_guest_types — the run state is
--            inventory data, which the handler reads under view:vm for a VM and
--            view:container for a container.
--
-- Guest tasks only: is_guest_upid, never vmid alone, because InsertTaskHistory
-- stamps a vmid on any numeric worker id — cephdestroyosd's is an OSD number,
-- so "destroy OSD 113" would otherwise read as something done to VM 113.
-- audit_log.vmid is already gated the same way at insert.
--
-- guest_audit is three branches rather than one OR, so each reads its own
-- index — the vmid arm (cluster_id, vmid), the clone arm the action index, the
-- HA arm the resource_type index — instead of the planner settling on the
-- whole cluster's rows. The second and third exclude the first's rows, so no
-- row is read twice. The Proxmox-duplicate probe is scoped to the same guest
-- by parameter, not by correlation: correlated on the UPID alone, the planner
-- hashes every Nexara row the install has ever written, on every request.
--
-- after/after_inclusive is the cutoff guestHistoryCutoff derived from
-- ListGuestLifecycleEvents: inclusive from a birth, so the create or clone is
-- the first entry; exclusive after a destroy, so the previous guest's last act
-- is not this one's first.
--
-- category is derived HERE, once, so it can be filtered on before the page is
-- cut; the handler's guestHistoryCategories lists the same values (pinned by
-- TestGuestHistoryCategoryVocabulary). Nexara's own action names are matched
-- before the Proxmox worker type, because they are the more specific of the
-- two: DRS asks an HA-managed guest to migrate through an hamigrate task, and
-- that is a migration. A start or stop of an HA-managed guest runs as hastart
-- or hastop, from Nexara or the Proxmox GUI alike, and is a power action; 'ha'
-- is what is left — the guest's HA resource itself. Unmatched is 'other' —
-- still listed, under its own action name.
--
-- The page is cut on narrow rows, and only its rows are joined to their audit
-- details, task outcome and user: a guest with ten thousand entries carries
-- ten thousand jsonb blobs through the sort otherwise. total is count(*) OVER
-- the filtered set, computed before LIMIT — the Items and the Total come from
-- one query and so cannot be counted under different filters.
-- name: ListGuestHistory :many
WITH guest_audit AS (
    SELECT a.id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid, a.user_id,
           a.details
    FROM audit_log a
    WHERE a.cluster_id = sqlc.arg('cluster_id')
      AND a.vmid = sqlc.arg('vmid')::int
    UNION ALL
    SELECT a.id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid, a.user_id,
           a.details
    FROM audit_log a
    WHERE a.action IN ('clone', 'clone-to-template')
      AND a.cluster_id = sqlc.arg('cluster_id')
      AND a.vmid IS DISTINCT FROM sqlc.arg('vmid')::int
      AND a.details->>'new_id' = sqlc.arg('vmid')::int::text
    UNION ALL
    SELECT a.id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid, a.user_id,
           a.details
    FROM audit_log a
    WHERE a.resource_type = 'ha_resource'
      AND a.cluster_id = sqlc.arg('cluster_id')
      AND a.vmid IS DISTINCT FROM sqlc.arg('vmid')::int
      AND a.resource_id IN ('vm:' || sqlc.arg('vmid')::int::text, 'ct:' || sqlc.arg('vmid')::int::text)
), entries AS (
    SELECT ('audit:' || g.id::text)::text                AS entry_id,
           'audit'::text                                 AS kind,
           g.id                                          AS ref_id,
           g.created_at                                  AS occurred_at,
           g.source                                      AS source,
           g.action                                      AS action,
           g.resource_type                               AS resource_type,
           g.resource_id                                 AS resource_id,
           COALESCE(g.vmid, sqlc.arg('vmid')::int)::int  AS vmid,
           g.user_id                                     AS user_id,
           COALESCE(g.details->>'upid', '')::text        AS upid,
           COALESCE(g.details->>'node', '')::text        AS node,
           COALESCE(g.details->>'status', '')::text      AS recorded_status,
           ''::text                                      AS from_status,
           ''::text                                      AS to_status,
           NULL::timestamptz                             AS last_seen_at
    FROM guest_audit g
    WHERE NOT (g.source = 'proxmox' AND g.details->>'upid' IS NOT NULL AND EXISTS (
            SELECT 1 FROM audit_log b
            WHERE b.cluster_id = sqlc.arg('cluster_id')
              AND b.vmid = sqlc.arg('vmid')::int
              AND b.source <> 'proxmox'
              AND b.details->>'upid' = g.details->>'upid'))
    UNION ALL
    SELECT ('task:' || t.upid)::text, 'task', NULL::uuid, t.started_at, t.source, t.task_type,
           'task', '', t.vmid, t.user_id, t.upid, t.node, '', '', '', NULL
    FROM task_history t
    WHERE sqlc.arg('include_tasks')::bool
      AND t.cluster_id = sqlc.arg('cluster_id')
      AND t.vmid = sqlc.arg('vmid')::int
      AND is_guest_upid(t.upid)
      AND NOT EXISTS (SELECT 1 FROM audit_log a
                      WHERE a.details->>'upid' = t.upid AND a.cluster_id = t.cluster_id)
    UNION ALL
    SELECT ('task:' || r.upid)::text, 'task', NULL::uuid, r.started_at, r.source, r.task_type,
           'task', '', r.vmid, r.user_id, r.upid, r.node, '', '', '', NULL
    FROM task_history_archive r
    WHERE sqlc.arg('include_tasks')::bool
      AND r.cluster_id = sqlc.arg('cluster_id')
      AND r.vmid = sqlc.arg('vmid')::int
      AND NOT EXISTS (SELECT 1 FROM audit_log a
                      WHERE a.details->>'upid' = r.upid AND a.cluster_id = r.cluster_id)
    UNION ALL
    SELECT ('status:' || s.id::text)::text, 'status', s.id, s.observed_at, 'detected', 'status_change',
           'status', '', s.vmid, NULL::uuid, '', s.node, '', s.from_status, s.to_status, s.last_seen_at
    FROM guest_status_changes s
    WHERE s.cluster_id = sqlc.arg('cluster_id')
      AND s.vmid = sqlc.arg('vmid')::int
      AND s.explained = false
      AND s.guest_type = ANY(sqlc.arg('status_guest_types')::text[])
), categorized AS (
    SELECT e.*,
           CASE
               WHEN e.kind = 'status' THEN 'power'
               WHEN e.action LIKE 'console%' THEN 'console'
               WHEN e.action LIKE 'drs\_%' OR e.action LIKE '%migrat%'
                 OR split_part(e.upid, ':', 6) IN ('qmigrate', 'vzmigrate', 'hamigrate', 'harelocate')
                 THEN 'migration'
               WHEN e.action LIKE '%snapshot%' OR e.action LIKE '%rollback%'
                 OR split_part(e.upid, ':', 6) IN ('qmsnapshot', 'qmdelsnapshot', 'qmrollback',
                                                   'vzsnapshot', 'vzdelsnapshot', 'vzrollback') THEN 'snapshot'
               WHEN e.action LIKE '%backup%' OR e.action LIKE '%restore%'
                 OR split_part(e.upid, ':', 6) IN ('vzdump', 'qmrestore', 'vzrestore') THEN 'backup'
               WHEN e.action LIKE '%clone%'
                 OR split_part(e.upid, ':', 6) IN ('qmclone', 'vzclone') THEN 'clone'
               WHEN e.action IN ('create', 'destroy', 'vm_import', 'convert-to-template', 'convert_template')
                 OR split_part(e.upid, ':', 6) IN ('qmcreate', 'vzcreate', 'qmdestroy', 'vzdestroy',
                                                   'qmtemplate', 'vztemplate') THEN 'lifecycle'
               WHEN e.action IN ('start', 'stop', 'shutdown', 'reboot', 'reset', 'suspend', 'resume', 'pause')
                 OR split_part(e.upid, ':', 6) IN ('qmstart', 'qmstop', 'qmshutdown', 'qmreboot', 'qmreset',
                                                   'qmsuspend', 'qmresume', 'qmpause', 'vzstart', 'vzstop',
                                                   'vzshutdown', 'vzreboot', 'vzsuspend', 'vzresume',
                                                   'hastart', 'hastop') THEN 'power'
               WHEN e.resource_type = 'ha_resource' OR split_part(e.upid, ':', 6) LIKE 'ha%' THEN 'ha'
               WHEN e.action IN ('disk_move', 'move_disk', 'volume_move')
                 OR split_part(e.upid, ':', 6) IN ('qmmove', 'move_volume') THEN 'migration'
               WHEN e.action LIKE '%config%' OR e.action LIKE 'disk\_%' OR e.action LIKE 'media\_%'
                 OR e.action IN ('set_pool', 'resize')
                 OR split_part(e.upid, ':', 6) IN ('qmconfig', 'resize') THEN 'config'
               ELSE 'other'
           END::text AS category
    FROM entries e
), page AS (
    SELECT c.*, count(*) OVER () AS total
    FROM categorized c
    WHERE (sqlc.narg('after')::timestamptz IS NULL
           OR c.occurred_at > sqlc.narg('after')::timestamptz
           OR (sqlc.arg('after_inclusive')::bool AND c.occurred_at = sqlc.narg('after')::timestamptz))
      AND (sqlc.narg('since')::timestamptz IS NULL OR c.occurred_at >= sqlc.narg('since')::timestamptz)
      AND (sqlc.narg('category')::text IS NULL OR c.category = sqlc.narg('category')::text)
      AND (sqlc.narg('source')::text IS NULL OR c.source = sqlc.narg('source')::text)
    ORDER BY c.occurred_at DESC, c.entry_id DESC
    LIMIT sqlc.arg('row_limit')::int OFFSET sqlc.arg('row_offset')::int
)
SELECT p.entry_id, p.kind, p.occurred_at, p.source, p.action, p.resource_type, p.resource_id, p.vmid,
       p.user_id, p.upid, p.node, p.recorded_status, p.from_status, p.to_status, p.last_seen_at,
       p.category, p.total,
       COALESCE(ad.details, '{}'::jsonb)                      AS details,
       COALESCE(u.email, '')::text                            AS user_email,
       COALESCE(u.display_name, '')::text                     AS user_display_name,
       COALESCE(th.status, ar.status, '')::text               AS task_status,
       COALESCE(th.exit_status, ar.exit_status, '')::text     AS task_exit_status,
       COALESCE(th.description, ar.description, '')::text     AS task_description,
       COALESCE(th.task_type, ar.task_type, '')::text         AS task_type,
       COALESCE(th.node, ar.node, '')::text                   AS task_node,
       th.progress                                            AS task_progress,
       COALESCE(th.finished_at, ar.finished_at)               AS task_finished_at
FROM page p
LEFT JOIN audit_log ad
       ON p.kind = 'audit' AND ad.id = p.ref_id
LEFT JOIN task_history th
       ON p.upid <> '' AND th.upid = p.upid AND th.cluster_id = sqlc.arg('cluster_id')
LEFT JOIN task_history_archive ar
       ON p.upid <> '' AND ar.upid = p.upid AND ar.cluster_id = sqlc.arg('cluster_id')
LEFT JOIN users u ON u.id = p.user_id
ORDER BY p.occurred_at DESC, p.entry_id DESC;
