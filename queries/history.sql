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
--   node_status_changes  a node's status changes, by trigger (000128).
--   guest_node_changes   a guest seen on another node than before, by
--                        trigger (000129): a node's arrivals and departures.
--   cluster_object_      a change Nexara saw in Proxmox — the replication
--   changes (000119)     trigger, the Ceph pass, the observer — listed once
--                        classified as explained by nothing on record, and
--                        only of the types the caller may view
--                        (observed_types). Entry kind observed. A row naming
--                        no cluster (000125, a PBS server's) is a global
--                        kind's alone.
--
-- How to add a source keyed as audit_log is — cluster_id, resource_type,
-- resource_id, and vmid for a guest — as cluster_object_changes is: one arm in
-- entries per family, under its own entry kind, each capped at row_cap and
-- bounded by since and until, and its row joined in the final SELECT for what
-- the page shows of it. The guests' arm reads cluster_id = cluster_id AND vmid
-- = ANY(vmids) with the floor, since and guest_workers (no UPID:
-- history_worker_ok(NULL, ...)), subject the vmid; the objects' arm reads each
-- object by its own index, minus the rows the guests' arm keeps.
-- TestGuestHistory's other-cluster block then gets a row of it for each arm.
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
--              a list key   the value is one element of a list in a details
--                           key (an ACL's users), split as Proxmox splits a
--                           list: on commas, semicolons and white space. Read
--                           by resource type alone, so for a type with few
--                           rows.
--              whole        every row of the type, whatever its id: a tab's
--                           History, of all the objects of its kinds. Read by
--                           (resource_type, created_at) within the cluster,
--                           so for a type of configuration objects, which
--                           are edited rarely.
--            objects_node, when set, drops the objects' rows that ran on
--            another node: a row's node is its UPID's where it has one, its
--            details.node otherwise, and a row naming none stays.
--            The keyed objects of one key and value are read in one scan,
--            whatever their types: a node's nine kinds of part all name it in
--            details.node, and the GIN lookup of a value that common costs the
--            same for each type — at 1M audit rows, 30 ms a type. Each
--            selector returns whole rows, and a row two of them name is folded
--            by its id (DISTINCT ON): one read of each row, not its id and
--            then the row.
--
-- A kind may hide shapes of row from what its objects select (hide_*,
-- HistoryKind.Hide): the rows of a resource type with an action, and, when a
-- details key is named, whose details hold the value under it. They are the
-- objects' rows only — a copy of another row, which the kind would list twice.
--
-- A kind may also name a node (node_name, HistorySelection.Node), whose own
-- records are then its: the tasks that ran on it — audit rows by the node in
-- their UPID, over the expression index built after startup, and tasks with no
-- audit row by task_history.node — its status changes, and the guests that
-- arrived on it or left it. A guest's task is one only with include_guests: a
-- busy node runs thousands of guest tasks, which would bury its own. Any other
-- row that names a guest is left out without include_guests too, but for the
-- arrivals and departures, which are the node's own. A status change through
-- "unknown" — pvestatd not reporting — that is back within two minutes is no
-- change, and one younger than that is not shown yet. An arrival across a
-- create, restore, clone or destroy of the VMID is a new guest, not a move: the
-- audit rows of the move's window say, read by time (OFFSET 0 keeps the window
-- the subquery's whole condition) and no further back than an hour. Asked by
-- VMID too, a generic plan read every row of the VMID for each move: a second
-- for a busy node's week. A window is minutes long: a move is recorded only
-- within minutes of the guest going (000129), and an hour is the bound for one
-- whose node stopped reporting it long before it showed up elsewhere. A move is
-- a guest's arrival or departure only with a migration of its VMID on record —
-- qmigrate, vzmigrate, an HA migrate or relocate, a remote migration's tunnel —
-- that succeeded, by its archived or live task row (as TaskSucceeded reads a
-- stopped one), ended between the guest's last sighting on its old node less
-- five minutes and its sighting on the new one plus five, and went to that
-- node where its audit row names a target (target, target_node). It is the end
-- that is bounded: a long migration begins well before the guest leaves; its
-- audit row is looked for in the day before. A failed one, the VMID's previous
-- occupant's or one elsewhere is no evidence, and any other move is the VMID
-- seen on another node (now_on), since a new guest on a VMID the collector had
-- not let go of looks the same. from_status and to_status carry its nodes.
--
-- or, alone, the whole cluster (whole_cluster, HistoryKind.WholeCluster): every
-- row of every source filed under the path's cluster, none of them a guest's
-- or an object's — a cluster's History. Rows of one (resource type, action)
-- repeated in bursts — the sync failures of an unreachable cluster — are read
-- as runs (collapse_*: one entry per run, kind run, a gap longer than the
-- pair's starting the next), and each source — each pair's rows, of which the
-- oldest run may then be only the newest part — is read newest first up to
-- row_cap rows when the handler sets one, so a cluster's whole audit trail is
-- never read to show one page of it. The audit arm reads by the (cluster_id,
-- created_at) index built after startup, through a row comparison only that
-- index can serve: written cluster_id = X with an ORDER BY created_at, a
-- generic plan — which takes X for an average cluster — walks the created_at
-- index instead, through every other cluster's rows, and a small cluster's
-- History in a big install read all 1M of them.
-- The runs are read per pair (the LIMIT keeps the lateral a lateral), and the
-- collapsed pairs are left out of the audit arm by a hashed NOT IN, a filter
-- the index scan keeps its order through, where NOT EXISTS became an anti join
-- the planner sorted after. In any other read the capped arms are LIMIT 0,
-- which starts nothing under it: their whole_cluster gate sits below a
-- generic plan's parallel scan, which starts its workers first — 20 ms a read.
-- Each capped arm holds its rows to the caller's guest types itself, as the
-- page does (status_guest_types), so a cap counts only rows the caller sees.
--
-- Any kind may cap its read so (row_cap, HistoryKind.RowCap: a folder's and a
-- node's do): each source newest first up to row_cap rows, the guests' rows,
-- the objects' and a node's tasks each read to the cap before their arm takes
-- its newest row_cap of them. The guests' own rows are read per guest by
-- (cluster_id, vmid, created_at) from where their row_cap newest rows begin,
-- found from that index alone (vmid_floor): a folder of 500 busy guests reads
-- its newest 20,000 rows, not all 700,000. A task arm caps the tasks it
-- searches, not the few with no audit row it finds. The object reads apply the
-- include_guests and guest-type tests the page applies, so the cap counts only
-- rows the page could show.
--
-- A capped read is whole back to searched_from (horizon): a microsecond past
-- the latest of the points where a source that reached its cap stopped, and
-- of the guests' floor, since rows at the point itself may be cut. It lists
-- only what is at or after it, with that count as total, so each stretch is
-- whole and the next — the same read with until (end_time, exclusive, in
-- every arm beside since) set to searched_from — begins where it ends. Runs
-- fold only the rows at or after it: a burst across it is a run on each side.
-- Each source is counted where its cap is applied. The source filter is
-- applied in every arm, so a cap counts only that source's rows — Proxmox's
-- together with the rows that may be their Nexara twins, which the duplicate
-- probe needs — but for the guests' floor, which reads its index alone and so
-- counts every source: under a source filter a folder's read can stop being
-- whole sooner than the named source's own rows would make it. The type
-- filter is not applied in the arms: a category is worked out once per kind
-- of row after them (category_map), and per row in each arm it cost four
-- times as much.
--
-- Every branch reads one cluster, a REQUIRED argument: the route's, which its
-- cluster-scoped gate authorized. The one exception is a global kind's objects
-- (global, HistoryKind.Global): read in every cluster and in none, because
-- their ids are unique across Nexara — a PBS server's, Veeam's, a firewall
-- template's, or a details key whose values are such ids — and their route's
-- gate is all GLOBAL. A global kind has no guests. Each entry carries its own
-- cluster_id, which its task outcome is joined on.

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
--            UPID and is on the page: the collector's dedupe can lose a race
--            with a handler's own insert, and the two rows are one task, the
--            Nexara one the one that says who asked — but a filter of type or
--            source that turns the Nexara row away keeps the Proxmox one, which
--            it would otherwise lose with it. A twin outside the selection drops nothing — a
--            node's History keeps a guest task it selects by details.node even
--            when the Nexara row of it, filed under the guest, is not its own.
--            An entry that names a guest outside the guests, filed under none
--            of them — one filed into a folder, a task run on a node, any of a
--            whole cluster's — is the caller's only under that guest's own
--            view permission, as the guests are (viewableGuests): its type in
--            status_guest_types, or both types for a guest the inventory no
--            longer holds. Checked on the page, for every source alike. An
--            entry filed under one of the guests is that guest's, as the clone
--            into it, which names its source, is.
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
-- — the vmid arm (cluster_id, vmid, created_at), the clone arm the clones by
-- new_id, the HA arm the resource_type index, the object arm theirs, all built
-- after startup (db.EnsureHistoryIndexes; it works without them, slower) —
-- instead of the planner settling on the whole cluster's rows. Each guest arm
-- is bounded by the later of its guest's cutoff — or the cutoff floor, where
-- an arm reads every guest at once — and since in one condition the index can
-- range over: as two "IS NULL OR" tests it read every row of each
-- guest, a folder's 7 days 11,249 rows to keep 1,482. Each later arm excludes the rows an
-- earlier one keeps, so no row is read twice; a guest's row guest_workers turns
-- away is still an object's to take. The object arm reads each object by its
-- index: (resource_type, resource_id) for its own rows, GIN details @> {key:
-- value} for a details key, once for every type keyed by that value — a filter
-- on details->>key read every row of the type — and (resource_type,
-- created_at) for a whole type. "vmid IS NOT NULL" lets the
-- vmid arms use their partial indexes whatever the plan: a generic plan cannot
-- prove it from "= ANY(<parameter>)".
-- The Proxmox-duplicate probe reads the selection
-- itself, never audit_log: probed there by UPID, a generic plan scanned the
-- whole cluster's rows on every request. It is an IN over the page's Nexara
-- UPIDs, hashed once, rather than an EXISTS run for every row.
--
-- subject is the guest an entry is filed under — the clone's target, the HA
-- sid's guest — and picks the cutoff it is held to: cutoff_after and
-- cutoff_inclusive, parallel to vmids, from guestHistoryCutoff. Inclusive from
-- a birth, so the create or clone is the first entry; exclusive after a
-- destroy, so the previous guest's last act is not this one's first. The zero
-- time is no cutoff. An object's row has no subject and no cutoff, unless it
-- names one of the guests — a row of the guest's that guest_workers turned
-- away and an object took — which is then that guest's and held to its cutoff,
-- so a previous holder of the VMID is not the object's either. That is the
-- kind's choice (objects_ignore_cutoff, HistoryKind.ObjectsIgnoreGuestCutoff):
-- right where the object is the guest's — its HA resource — and wrong where a
-- row stands on its own, as a task that ran on a node did, whoever held the
-- VMID then.
--
-- since, and cutoff_floor — the earliest of the guests' cutoffs, which no
-- guest's row comes before; NULL when a guest has none — are repeated in every
-- arm: entries is read twice, so it is materialized, and a bound applied to it
-- reaches no arm. A 24-hour window on the busiest guest on dev read all its
-- rows otherwise. In the guest arms both are written "IS NULL OR": a bare range
-- on a parameter tempts a generic plan to AND the arm's index with the
-- created_at index, which read all of dev's 32k audit rows for a 30-row guest.
-- In the object arm's index reads since is a bare range against -infinity
-- instead, which the (resource_type, resource_id, created_at) index takes as a
-- bound: a node's 24 hours at 10x dev, 60-70 ms on a generic plan as "IS NULL
-- OR", 14 ms so. Each entry finds
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
-- job's own task row, joined like any other, gives the rest. A task's
-- outcome is the archive's before task_history's, as its cutoff is
-- (ListGuestLifecycleEvents): what the collector settled before the prune
-- moved it, not a row filed under its UPID since. total is
-- count(*) OVER the filtered set, computed before LIMIT — the Items and the
-- Total come from one query and so cannot be counted under different filters.
-- An empty page of a capped read — past its end, or of a stretch with nothing
-- in it — is one row with no entry (entry_id ''), there only to carry
-- searched_from.
-- name: ListHistory :many
WITH guests AS MATERIALIZED (
    SELECT v.vmid, a.after, i.inclusive
    FROM unnest(sqlc.arg('vmids')::int[]) WITH ORDINALITY AS v(vmid, n)
    JOIN unnest(sqlc.arg('cutoff_after')::timestamptz[]) WITH ORDINALITY AS a(after, n) ON a.n = v.n
    JOIN unnest(sqlc.arg('cutoff_inclusive')::bool[]) WITH ORDINALITY AS i(inclusive, n) ON i.n = v.n
), guest_cutoffs AS MATERIALIZED (
    -- The guests and their cutoffs as one map, which each entry looks itself
    -- up in: joined to the guests, the entries ran a nested loop over every
    -- guest for each of them, 21M comparisons for a 500-guest folder's week.
    SELECT COALESCE(jsonb_object_agg(g.vmid::text, jsonb_build_object('after', g.after, 'inclusive', g.inclusive)),
                    '{}'::jsonb) AS m
    FROM guests g
), objects AS (
    SELECT t.resource_type, r.resource_id, k.detail_key, v.value, l.in_list, w.whole
    FROM unnest(sqlc.arg('object_types')::text[]) WITH ORDINALITY AS t(resource_type, n)
    JOIN unnest(sqlc.arg('object_resource_ids')::text[]) WITH ORDINALITY AS r(resource_id, n) ON r.n = t.n
    JOIN unnest(sqlc.arg('object_keys')::text[]) WITH ORDINALITY AS k(detail_key, n) ON k.n = t.n
    JOIN unnest(sqlc.arg('object_values')::text[]) WITH ORDINALITY AS v(value, n) ON v.n = t.n
    JOIN unnest(sqlc.arg('object_in_list')::bool[]) WITH ORDINALITY AS l(in_list, n) ON l.n = t.n
    JOIN unnest(sqlc.arg('object_whole')::bool[]) WITH ORDINALITY AS w(whole, n) ON w.n = t.n
), hidden AS (
    SELECT t.resource_type, a.action, k.detail_key, v.value AS detail_value
    FROM unnest(sqlc.arg('hide_types')::text[]) WITH ORDINALITY AS t(resource_type, n)
    JOIN unnest(sqlc.arg('hide_actions')::text[]) WITH ORDINALITY AS a(action, n) ON a.n = t.n
    JOIN unnest(sqlc.arg('hide_keys')::text[]) WITH ORDINALITY AS k(detail_key, n) ON k.n = t.n
    JOIN unnest(sqlc.arg('hide_values')::text[]) WITH ORDINALITY AS v(value, n) ON v.n = t.n
), rules AS (
    SELECT c.category, f.field, p.pattern, c.n AS ord
    FROM unnest(sqlc.arg('rule_categories')::text[]) WITH ORDINALITY AS c(category, n)
    JOIN unnest(sqlc.arg('rule_fields')::text[]) WITH ORDINALITY AS f(field, n) ON f.n = c.n
    JOIN unnest(sqlc.arg('rule_patterns')::text[]) WITH ORDINALITY AS p(pattern, n) ON p.n = c.n
), collapse AS (
    SELECT t.resource_type, a.action, make_interval(secs => g.gap) AS gap
    FROM unnest(sqlc.arg('collapse_types')::text[]) WITH ORDINALITY AS t(resource_type, n)
    JOIN unnest(sqlc.arg('collapse_actions')::text[]) WITH ORDINALITY AS a(action, n) ON a.n = t.n
    JOIN unnest(sqlc.arg('collapse_gaps')::int[]) WITH ORDINALITY AS g(gap, n) ON g.n = t.n
), vmid_floor AS MATERIALIZED (
    -- Where the guests' row_cap newest rows begin, read from the index alone:
    -- the vmid arm then reads that many rows, not every row of a folder's
    -- guests. None under a worker filter, which the index cannot apply.
    SELECT min(f.created_at) AS at
    FROM (SELECT x.created_at
          FROM guests g
          CROSS JOIN LATERAL (
              SELECT a.created_at
              FROM audit_log a
              WHERE cardinality(sqlc.arg('guest_workers')::text[]) = 0
                AND a.cluster_id = sqlc.arg('cluster_id')::uuid
                AND a.vmid IS NOT NULL
                AND a.vmid = g.vmid
                AND a.created_at >= GREATEST(g.after, COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz))
                AND a.created_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
              ORDER BY a.created_at DESC
              LIMIT COALESCE(NULLIF(sqlc.arg('row_cap')::int, 0), 0)) x
          ORDER BY x.created_at DESC
          LIMIT COALESCE(NULLIF(sqlc.arg('row_cap')::int, 0), 0)) f
    HAVING count(*) >= NULLIF(sqlc.arg('row_cap')::int, 0)
), audit AS (
    (SELECT x.id, x.cluster_id, x.created_at, x.source, x.action, x.resource_type, x.resource_id, x.vmid, x.user_id,
            x.upid, x.node, x.recorded_status, x.subject, 'vmid'::text AS src
     FROM guests g
     CROSS JOIN LATERAL (
         SELECT a.id, a.cluster_id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid,
                a.user_id, a.details->>'upid' AS upid, a.details->>'node' AS node,
                a.details->>'status' AS recorded_status, a.vmid AS subject
         FROM audit_log a
         WHERE a.cluster_id = sqlc.arg('cluster_id')::uuid
           AND a.vmid IS NOT NULL
           AND a.vmid = g.vmid
           AND history_worker_ok(a.details->>'upid', sqlc.arg('guest_workers')::text[])
           AND (sqlc.narg('source')::text IS NULL OR a.source = sqlc.narg('source')::text
                OR sqlc.narg('source')::text = 'proxmox' AND a.details ? 'upid')
           AND a.created_at >= GREATEST(g.after, COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz),
                                        COALESCE((SELECT f.at FROM vmid_floor f), '-infinity'::timestamptz))
           AND a.created_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
         ORDER BY a.created_at DESC
         LIMIT NULLIF(sqlc.arg('row_cap')::int, 0)) x
     ORDER BY x.created_at DESC
     LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
    UNION ALL
    (SELECT x.id, x.cluster_id, x.created_at, x.source, x.action, x.resource_type, x.resource_id, x.vmid, x.user_id,
            x.upid, x.node, x.recorded_status, x.subject, 'clone'::text
     FROM guests g
     CROSS JOIN LATERAL (
         SELECT a.id, a.cluster_id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid,
                a.user_id, a.details->>'upid' AS upid, a.details->>'node' AS node,
                a.details->>'status' AS recorded_status, (a.details->>'new_id')::int AS subject
         FROM audit_log a
         WHERE a.action IN ('clone', 'clone-to-template')
           AND a.cluster_id = sqlc.arg('cluster_id')::uuid
           AND NOT COALESCE(a.vmid = ANY(sqlc.arg('vmids')::int[]), false)
           AND a.details->>'new_id' = g.vmid::text
           AND history_worker_ok(a.details->>'upid', sqlc.arg('guest_workers')::text[])
           AND (sqlc.narg('source')::text IS NULL OR a.source = sqlc.narg('source')::text
                OR sqlc.narg('source')::text = 'proxmox' AND a.details ? 'upid')
           AND a.created_at >= GREATEST(g.after, COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz))
           AND a.created_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
         ORDER BY a.created_at DESC
         LIMIT NULLIF(sqlc.arg('row_cap')::int, 0)) x
     ORDER BY x.created_at DESC
     LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
    UNION ALL
    (SELECT a.id, a.cluster_id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid, a.user_id,
            a.details->>'upid', a.details->>'node', a.details->>'status',
            split_part(a.resource_id, ':', 2)::int, 'ha'::text
     FROM audit_log a
     WHERE a.resource_type = 'ha_resource'
       AND a.cluster_id = sqlc.arg('cluster_id')::uuid
       AND NOT COALESCE(a.vmid = ANY(sqlc.arg('vmids')::int[]), false)
       AND a.resource_id = ANY(ARRAY(SELECT p || v FROM unnest(sqlc.arg('vmids')::int[]) AS v,
                                                      unnest('{vm:,ct:}'::text[]) AS p))
       AND history_worker_ok(a.details->>'upid', sqlc.arg('guest_workers')::text[])
       AND (sqlc.narg('source')::text IS NULL OR a.source = sqlc.narg('source')::text
            OR sqlc.narg('source')::text = 'proxmox' AND a.details ? 'upid')
       AND a.created_at >= GREATEST(COALESCE(sqlc.narg('cutoff_floor')::timestamptz, '-infinity'::timestamptz),
                            COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz))
       AND a.created_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY a.created_at DESC
     LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
    UNION ALL
    (SELECT o.id, o.cluster_id, o.created_at, o.source, o.action, o.resource_type, o.resource_id, o.vmid, o.user_id,
            o.upid, o.node, o.recorded_status, o.subject, 'objects'::text
     FROM (
    SELECT DISTINCT ON (a.id)
           a.id, a.cluster_id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid, a.user_id,
           a.upid, a.node, a.recorded_status,
           CASE WHEN sqlc.arg('objects_ignore_cutoff')::bool THEN NULL::int
                WHEN a.vmid = ANY(sqlc.arg('vmids')::int[]) THEN a.vmid
                WHEN a.action IN ('clone', 'clone-to-template')
                     AND a.new_id = ANY(sqlc.arg('vmids')::int[]::text[])
                    THEN a.new_id::int
                WHEN a.resource_type = 'ha_resource'
                     AND a.resource_id = ANY(ARRAY(SELECT p || v FROM unnest(sqlc.arg('vmids')::int[]) AS v,
                                                                    unnest('{vm:,ct:}'::text[]) AS p))
                    THEN split_part(a.resource_id, ':', 2)::int
           END AS subject
    FROM (
          (SELECT b.id, b.cluster_id, b.created_at, b.source, b.action, b.resource_type, b.resource_id, b.vmid,
                  b.user_id, b.details->>'upid' AS upid, b.details->>'node' AS node,
                  b.details->>'status' AS recorded_status, b.details->>'new_id' AS new_id
           FROM audit_log b
           WHERE sqlc.arg('node_name')::text <> ''
             AND b.details ? 'upid'
             AND b.cluster_id = sqlc.arg('cluster_id')::uuid
             AND split_part(b.details->>'upid', ':', 2) = sqlc.arg('node_name')::text
             AND (sqlc.arg('include_guests')::bool OR NOT is_guest_upid(b.details->>'upid'))
             AND (sqlc.narg('source')::text IS NULL OR b.source = sqlc.narg('source')::text
                  OR sqlc.narg('source')::text = 'proxmox' AND b.details ? 'upid')
             AND (b.vmid IS NULL OR b.vmid = ANY(sqlc.arg('vmids')::int[])
                 OR NOT sqlc.arg('objects_ignore_cutoff')::bool
                    AND (b.action IN ('clone', 'clone-to-template')
                         AND b.details->>'new_id' = ANY(sqlc.arg('vmids')::int[]::text[])
                         OR b.resource_type = 'ha_resource'
                            AND b.resource_id = ANY(ARRAY(SELECT p || v FROM unnest(sqlc.arg('vmids')::int[]) AS v,
                                                                           unnest('{vm:,ct:}'::text[]) AS p)))
                 OR sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
                 OR EXISTS (SELECT 1 FROM vms v WHERE v.cluster_id = b.cluster_id AND v.vmid = b.vmid
                            AND v.type = ANY(sqlc.arg('status_guest_types')::text[])))
             AND b.created_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
             AND b.created_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
           ORDER BY b.created_at DESC
           LIMIT CASE WHEN sqlc.arg('node_name')::text <> '' THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
          UNION ALL
          SELECT m.id, m.cluster_id, m.created_at, m.source, m.action, m.resource_type, m.resource_id, m.vmid, m.user_id,
                 m.upid, m.node, m.recorded_status, m.new_id
          FROM objects o
          CROSS JOIN LATERAL (
              (SELECT b.id, b.cluster_id, b.created_at, b.source, b.action, b.resource_type, b.resource_id, b.vmid,
                      b.user_id, b.details->>'upid' AS upid, b.details->>'node' AS node,
                      b.details->>'status' AS recorded_status, b.details->>'new_id' AS new_id
               FROM audit_log b
               WHERE o.detail_key = '' AND NOT o.whole
                 AND b.resource_type = o.resource_type AND b.resource_id = o.value
                 AND (sqlc.arg('global')::bool OR b.cluster_id = sqlc.arg('cluster_id')::uuid)
                 AND (sqlc.arg('include_guests')::bool OR b.vmid IS NULL)
                 AND (sqlc.narg('source')::text IS NULL OR b.source = sqlc.narg('source')::text
                      OR sqlc.narg('source')::text = 'proxmox' AND b.details ? 'upid')
                 AND (b.vmid IS NULL OR b.vmid = ANY(sqlc.arg('vmids')::int[])
                 OR NOT sqlc.arg('objects_ignore_cutoff')::bool
                    AND (b.action IN ('clone', 'clone-to-template')
                         AND b.details->>'new_id' = ANY(sqlc.arg('vmids')::int[]::text[])
                         OR b.resource_type = 'ha_resource'
                            AND b.resource_id = ANY(ARRAY(SELECT p || v FROM unnest(sqlc.arg('vmids')::int[]) AS v,
                                                                           unnest('{vm:,ct:}'::text[]) AS p)))
                 OR sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
                 OR EXISTS (SELECT 1 FROM vms v WHERE v.cluster_id = b.cluster_id AND v.vmid = b.vmid
                            AND v.type = ANY(sqlc.arg('status_guest_types')::text[])))
                 AND b.created_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
                 AND b.created_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
                 AND NOT EXISTS (SELECT 1 FROM hidden h WHERE h.resource_type = b.resource_type AND h.action = b.action
                                   AND (h.detail_key = '' OR b.details->>h.detail_key = h.detail_value))
               ORDER BY b.created_at DESC
               LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
              UNION ALL
              (SELECT b.id, b.cluster_id, b.created_at, b.source, b.action, b.resource_type, b.resource_id, b.vmid,
                      b.user_id, b.details->>'upid' AS upid, b.details->>'node' AS node,
                      b.details->>'status' AS recorded_status, b.details->>'new_id' AS new_id
               FROM audit_log b
               WHERE o.whole
                 AND b.resource_type = o.resource_type
                 AND (sqlc.arg('global')::bool OR b.cluster_id = sqlc.arg('cluster_id')::uuid)
                 AND (sqlc.arg('include_guests')::bool OR b.vmid IS NULL)
                 AND (sqlc.narg('source')::text IS NULL OR b.source = sqlc.narg('source')::text
                      OR sqlc.narg('source')::text = 'proxmox' AND b.details ? 'upid')
                 AND (b.vmid IS NULL OR b.vmid = ANY(sqlc.arg('vmids')::int[])
                 OR NOT sqlc.arg('objects_ignore_cutoff')::bool
                    AND (b.action IN ('clone', 'clone-to-template')
                         AND b.details->>'new_id' = ANY(sqlc.arg('vmids')::int[]::text[])
                         OR b.resource_type = 'ha_resource'
                            AND b.resource_id = ANY(ARRAY(SELECT p || v FROM unnest(sqlc.arg('vmids')::int[]) AS v,
                                                                           unnest('{vm:,ct:}'::text[]) AS p)))
                 OR sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
                 OR EXISTS (SELECT 1 FROM vms v WHERE v.cluster_id = b.cluster_id AND v.vmid = b.vmid
                            AND v.type = ANY(sqlc.arg('status_guest_types')::text[])))
                 AND b.created_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
                 AND b.created_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
                 AND NOT EXISTS (SELECT 1 FROM hidden h WHERE h.resource_type = b.resource_type AND h.action = b.action
                                   AND (h.detail_key = '' OR b.details->>h.detail_key = h.detail_value))
               ORDER BY b.created_at DESC
               LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
              UNION ALL
              (SELECT b.id, b.cluster_id, b.created_at, b.source, b.action, b.resource_type, b.resource_id, b.vmid,
                      b.user_id, b.details->>'upid', b.details->>'node', b.details->>'status',
                      b.details->>'new_id'
               FROM audit_log b
               WHERE o.in_list
                 AND b.resource_type = o.resource_type
                 AND (o.resource_id = '' OR b.resource_id = o.resource_id)
                 AND o.value = ANY(regexp_split_to_array(btrim(b.details->>o.detail_key), '[,;\s]+'))
                 AND (sqlc.arg('global')::bool OR b.cluster_id = sqlc.arg('cluster_id')::uuid)
                 AND (sqlc.arg('include_guests')::bool OR b.vmid IS NULL)
                 AND (sqlc.narg('source')::text IS NULL OR b.source = sqlc.narg('source')::text
                      OR sqlc.narg('source')::text = 'proxmox' AND b.details ? 'upid')
                 AND (b.vmid IS NULL OR b.vmid = ANY(sqlc.arg('vmids')::int[])
                 OR NOT sqlc.arg('objects_ignore_cutoff')::bool
                    AND (b.action IN ('clone', 'clone-to-template')
                         AND b.details->>'new_id' = ANY(sqlc.arg('vmids')::int[]::text[])
                         OR b.resource_type = 'ha_resource'
                            AND b.resource_id = ANY(ARRAY(SELECT p || v FROM unnest(sqlc.arg('vmids')::int[]) AS v,
                                                                           unnest('{vm:,ct:}'::text[]) AS p)))
                 OR sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
                 OR EXISTS (SELECT 1 FROM vms v WHERE v.cluster_id = b.cluster_id AND v.vmid = b.vmid
                            AND v.type = ANY(sqlc.arg('status_guest_types')::text[])))
                 AND b.created_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
                 AND b.created_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
                 AND NOT EXISTS (SELECT 1 FROM hidden h WHERE h.resource_type = b.resource_type AND h.action = b.action
                                   AND (h.detail_key = '' OR b.details->>h.detail_key = h.detail_value))
               ORDER BY b.created_at DESC
               LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
          ) m
          UNION ALL
          SELECT m.id, m.cluster_id, m.created_at, m.source, m.action, m.resource_type, m.resource_id, m.vmid, m.user_id,
                 m.upid, m.node, m.recorded_status, m.new_id
          FROM (SELECT o.detail_key, o.value, o.resource_id, array_agg(o.resource_type) AS types
                FROM objects o
                WHERE o.detail_key <> '' AND NOT o.in_list
                GROUP BY o.detail_key, o.value, o.resource_id) o
          CROSS JOIN LATERAL (
              SELECT b.id, b.cluster_id, b.created_at, b.source, b.action, b.resource_type, b.resource_id, b.vmid,
                     b.user_id, b.details->>'upid' AS upid, b.details->>'node' AS node,
                     b.details->>'status' AS recorded_status, b.details->>'new_id' AS new_id
              FROM audit_log b
              WHERE (b.details @> jsonb_build_object(o.detail_key, o.value)
                     OR b.details @> CASE WHEN o.value ~ '^-?[0-9]{1,15}$'
                                          THEN jsonb_build_object(o.detail_key, o.value::bigint) END)
                AND b.details->>o.detail_key = o.value
                AND b.resource_type = ANY(o.types)
                AND (o.resource_id = '' OR b.resource_id = o.resource_id)
                AND (sqlc.arg('global')::bool OR b.cluster_id = sqlc.arg('cluster_id')::uuid)
                AND (sqlc.arg('include_guests')::bool OR b.vmid IS NULL)
                AND (sqlc.narg('source')::text IS NULL OR b.source = sqlc.narg('source')::text
                     OR sqlc.narg('source')::text = 'proxmox' AND b.details ? 'upid')
                AND (b.vmid IS NULL OR b.vmid = ANY(sqlc.arg('vmids')::int[])
                 OR NOT sqlc.arg('objects_ignore_cutoff')::bool
                    AND (b.action IN ('clone', 'clone-to-template')
                         AND b.details->>'new_id' = ANY(sqlc.arg('vmids')::int[]::text[])
                         OR b.resource_type = 'ha_resource'
                            AND b.resource_id = ANY(ARRAY(SELECT p || v FROM unnest(sqlc.arg('vmids')::int[]) AS v,
                                                                           unnest('{vm:,ct:}'::text[]) AS p)))
                 OR sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
                 OR EXISTS (SELECT 1 FROM vms v WHERE v.cluster_id = b.cluster_id AND v.vmid = b.vmid
                            AND v.type = ANY(sqlc.arg('status_guest_types')::text[])))
                AND b.created_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
                AND b.created_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
                AND NOT EXISTS (SELECT 1 FROM hidden h WHERE h.resource_type = b.resource_type AND h.action = b.action
                                  AND (h.detail_key = '' OR b.details->>h.detail_key = h.detail_value))
              -- No cap of its own: a bitmap read stops at no row count, and a
              -- LIMIT here turned the generic plan to the created_at index for
              -- its AND, 4x the heap rows. The arm's cap bounds what it keeps.
          ) m) a
    WHERE (cardinality(sqlc.arg('object_types')::text[]) > 0 OR sqlc.arg('node_name')::text <> '')
      AND NOT COALESCE(a.vmid = ANY(sqlc.arg('vmids')::int[])
                       AND history_worker_ok(a.upid, sqlc.arg('guest_workers')::text[]), false)
      AND NOT COALESCE(a.action IN ('clone', 'clone-to-template')
                       AND a.new_id = ANY(sqlc.arg('vmids')::int[]::text[])
                       AND history_worker_ok(a.upid, sqlc.arg('guest_workers')::text[]), false)
      AND NOT COALESCE(a.resource_type = 'ha_resource'
                       AND a.resource_id = ANY(ARRAY(SELECT p || v FROM unnest(sqlc.arg('vmids')::int[]) AS v,
                                                                      unnest('{vm:,ct:}'::text[]) AS p))
                       AND history_worker_ok(a.upid, sqlc.arg('guest_workers')::text[]), false)
      AND (sqlc.arg('objects_node')::text = ''
           OR COALESCE(NULLIF(split_part(a.upid, ':', 2), ''), a.node, '') IN ('', sqlc.arg('objects_node')::text))
     ) o
     ORDER BY o.created_at DESC
     LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
    UNION ALL
    (SELECT a.id, a.cluster_id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.vmid, a.user_id,
            a.details->>'upid', a.details->>'node', a.details->>'status', NULL::int, 'cluster'::text
     FROM audit_log a
     WHERE sqlc.arg('whole_cluster')::bool
       AND (sqlc.narg('source')::text IS NULL OR a.source = sqlc.narg('source')::text
            OR sqlc.narg('source')::text = 'proxmox' AND a.details ? 'upid')
       AND (a.cluster_id, a.created_at)
           >= (sqlc.arg('cluster_id')::uuid, COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz))
       AND (a.cluster_id, a.created_at) < (sqlc.arg('cluster_id')::uuid, COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz))
       AND (a.resource_type, a.action) NOT IN (SELECT k.resource_type, k.action FROM collapse k)
       AND (a.vmid IS NULL
            OR (sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
            OR EXISTS (SELECT 1 FROM vms v WHERE v.cluster_id = a.cluster_id AND v.vmid = a.vmid
                       AND v.type = ANY(sqlc.arg('status_guest_types')::text[]))))
     ORDER BY a.cluster_id DESC, a.created_at DESC
     LIMIT CASE WHEN sqlc.arg('whole_cluster')::bool THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
), run_rows AS (
    SELECT a.id, a.cluster_id, a.created_at, a.source, a.action, a.resource_type, a.resource_id, a.user_id,
           CASE WHEN a.created_at - lag(a.created_at) OVER w <= k.gap THEN 0 ELSE 1 END AS starts
    FROM collapse k
    CROSS JOIN LATERAL (
        SELECT b.id, b.cluster_id, b.created_at, b.source, b.action, b.resource_type, b.resource_id, b.user_id
        FROM audit_log b
        WHERE sqlc.arg('whole_cluster')::bool
          AND b.resource_type = k.resource_type AND b.action = k.action
          AND b.cluster_id = sqlc.arg('cluster_id')::uuid
          AND (sqlc.narg('source')::text IS NULL OR b.source = sqlc.narg('source')::text)
          AND b.created_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
          AND b.created_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
        ORDER BY b.created_at DESC, b.id DESC
        LIMIT NULLIF(sqlc.arg('row_cap')::int, 0)
    ) a
    WINDOW w AS (PARTITION BY a.resource_type, a.action ORDER BY a.created_at, a.id)
), tasks_raw AS MATERIALIZED (
    -- The tasks each task source searches for ones with no audit row: its
    -- newest row_cap, a cap on what it searches rather than on the few it
    -- finds — almost every task has its audit row, and a cap past the probe
    -- read them all. The guests' tasks and archive, the whole cluster's, a
    -- node's.
    (SELECT 'guest_tasks'::text AS src, t.upid, t.cluster_id, t.started_at, t.source, t.task_type,
            t.vmid AS vmid, t.vmid AS subject, t.user_id, t.node
     FROM task_history t
     WHERE sqlc.arg('include_tasks')::bool
       AND t.cluster_id = sqlc.arg('cluster_id')::uuid
       AND t.vmid IS NOT NULL
       AND t.vmid = ANY(sqlc.arg('vmids')::int[])
       AND is_guest_upid(t.upid)
       AND history_worker_ok(t.upid, sqlc.arg('guest_workers')::text[])
       AND (sqlc.narg('source')::text IS NULL OR t.source = sqlc.narg('source')::text)
       AND t.started_at >= GREATEST(COALESCE(sqlc.narg('cutoff_floor')::timestamptz, '-infinity'::timestamptz),
                            COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz))
       AND t.started_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY t.started_at DESC
     LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
    UNION ALL
    (SELECT 'guest_archive', r.upid, r.cluster_id, r.started_at, r.source, r.task_type, r.vmid, r.vmid, r.user_id, r.node
     FROM task_history_archive r
     WHERE sqlc.arg('include_tasks')::bool
       AND r.cluster_id = sqlc.arg('cluster_id')::uuid
       AND r.vmid = ANY(sqlc.arg('vmids')::int[])
       AND history_worker_ok(r.upid, sqlc.arg('guest_workers')::text[])
       AND (sqlc.narg('source')::text IS NULL OR r.source = sqlc.narg('source')::text)
       AND r.started_at >= GREATEST(COALESCE(sqlc.narg('cutoff_floor')::timestamptz, '-infinity'::timestamptz),
                            COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz))
       AND r.started_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY r.started_at DESC
     LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
    -- A task names a guest only when it is a guest task (is_guest_upid).
    UNION ALL
    (SELECT 'cluster_tasks', t.upid, t.cluster_id, t.started_at, t.source, t.task_type,
            CASE WHEN is_guest_upid(t.upid) THEN COALESCE(t.vmid, 0) ELSE 0 END, NULL::int, t.user_id, t.node
     FROM task_history t
     WHERE sqlc.arg('whole_cluster')::bool AND sqlc.arg('include_tasks')::bool
       AND t.cluster_id = sqlc.arg('cluster_id')::uuid
       AND (sqlc.narg('source')::text IS NULL OR t.source = sqlc.narg('source')::text)
       AND t.started_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
       AND t.started_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
       AND (NOT is_guest_upid(t.upid) OR t.vmid IS NULL
            OR (sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
            OR EXISTS (SELECT 1 FROM vms v WHERE v.cluster_id = t.cluster_id AND v.vmid = t.vmid
                       AND v.type = ANY(sqlc.arg('status_guest_types')::text[]))))
     ORDER BY t.started_at DESC
     LIMIT CASE WHEN sqlc.arg('whole_cluster')::bool THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
    UNION ALL
    (SELECT 'cluster_archive', r.upid, r.cluster_id, r.started_at, r.source, r.task_type, COALESCE(r.vmid, 0), NULL::int,
            r.user_id, r.node
     FROM task_history_archive r
     WHERE sqlc.arg('whole_cluster')::bool AND sqlc.arg('include_tasks')::bool
       AND r.cluster_id = sqlc.arg('cluster_id')::uuid
       AND (sqlc.narg('source')::text IS NULL OR r.source = sqlc.narg('source')::text)
       AND r.started_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
       AND r.started_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
       AND (r.vmid IS NULL
            OR (sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
            OR EXISTS (SELECT 1 FROM vms v WHERE v.cluster_id = r.cluster_id AND v.vmid = r.vmid
                       AND v.type = ANY(sqlc.arg('status_guest_types')::text[]))))
     ORDER BY r.started_at DESC
     LIMIT CASE WHEN sqlc.arg('whole_cluster')::bool THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
    UNION ALL
    (SELECT 'node_tasks', t.upid, t.cluster_id, t.started_at, t.source, t.task_type,
            CASE WHEN is_guest_upid(t.upid) THEN COALESCE(t.vmid, 0) ELSE 0 END, NULL::int, t.user_id, t.node
     FROM task_history t
     WHERE sqlc.arg('node_name')::text <> '' AND sqlc.arg('include_tasks')::bool
       AND t.cluster_id = sqlc.arg('cluster_id')::uuid
       AND t.node = sqlc.arg('node_name')::text
       AND (sqlc.arg('include_guests')::bool OR NOT is_guest_upid(t.upid))
       AND (NOT is_guest_upid(t.upid) OR t.vmid IS NULL
            OR sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
            OR EXISTS (SELECT 1 FROM vms v WHERE v.cluster_id = t.cluster_id AND v.vmid = t.vmid
                       AND v.type = ANY(sqlc.arg('status_guest_types')::text[])))
       AND (sqlc.narg('source')::text IS NULL OR t.source = sqlc.narg('source')::text)
       AND t.started_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
       AND t.started_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY t.started_at DESC
     LIMIT CASE WHEN sqlc.arg('node_name')::text <> '' THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
    UNION ALL
    (SELECT 'node_archive', r.upid, r.cluster_id, r.started_at, r.source, r.task_type, r.vmid, NULL::int, r.user_id, r.node
     FROM task_history_archive r
     WHERE sqlc.arg('node_name')::text <> '' AND sqlc.arg('include_tasks')::bool
       AND sqlc.arg('include_guests')::bool
       AND r.cluster_id = sqlc.arg('cluster_id')::uuid
       AND r.node = sqlc.arg('node_name')::text
       AND (sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
            OR EXISTS (SELECT 1 FROM vms v WHERE v.cluster_id = r.cluster_id AND v.vmid = r.vmid
                       AND v.type = ANY(sqlc.arg('status_guest_types')::text[])))
       AND (sqlc.narg('source')::text IS NULL OR r.source = sqlc.narg('source')::text)
       AND r.started_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
       AND r.started_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY r.started_at DESC
     LIMIT CASE WHEN sqlc.arg('node_name')::text <> '' THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
), entries_core AS MATERIALIZED (
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
           NULL::timestamptz                        AS last_seen_at,
           0                                        AS run_count,
           s.src                                    AS src
    FROM audit s
    UNION ALL
    SELECT ('task:' || r.upid)::text, 'task', NULL::uuid, r.cluster_id, r.started_at, r.source, r.task_type,
           'task', '', r.vmid, r.subject, r.user_id, r.upid, r.node, '', '', '', NULL, 0, 'task'::text
    FROM tasks_raw r
    WHERE NOT EXISTS (SELECT 1 FROM audit_log a
                      WHERE a.details->>'upid' = r.upid AND a.cluster_id = r.cluster_id)
    UNION ALL
    (SELECT ('status:' || s.id::text)::text, 'status', s.id, s.cluster_id, s.observed_at, 'detected', 'status_change',
            'status', '', s.vmid, s.vmid, NULL::uuid, '', s.node, '', s.from_status, s.to_status, s.last_seen_at, 0,
            'guest_status'::text
     FROM guest_status_changes s
     WHERE s.cluster_id = sqlc.arg('cluster_id')::uuid
       AND (sqlc.narg('source')::text IS NULL OR sqlc.narg('source')::text = 'detected')
       AND s.vmid = ANY(sqlc.arg('vmids')::int[])
       AND s.explained = false
       AND s.guest_type = ANY(sqlc.arg('status_guest_types')::text[])
       AND history_worker_ok(NULL, sqlc.arg('guest_workers')::text[])
       AND s.observed_at >= GREATEST(COALESCE(sqlc.narg('cutoff_floor')::timestamptz, '-infinity'::timestamptz),
                            COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz))
       AND s.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY s.observed_at DESC
     LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
    UNION ALL
    (SELECT ('backup:' || b.upid || ':' || b.vmid::text)::text, 'backup', NULL::uuid, b.cluster_id, b.started_at,
            b.source, 'vzdump', 'backup', '', b.vmid, b.vmid, b.user_id, b.upid, b.node, '', '', '', NULL, 0,
            'guest_backups'::text
     FROM guest_backups b
     WHERE sqlc.arg('include_tasks')::bool
       AND (sqlc.narg('source')::text IS NULL OR b.source = sqlc.narg('source')::text)
       AND b.cluster_id = sqlc.arg('cluster_id')::uuid
       AND b.vmid = ANY(sqlc.arg('vmids')::int[])
       AND history_worker_ok(b.upid, sqlc.arg('guest_workers')::text[])
       AND b.started_at >= GREATEST(COALESCE(sqlc.narg('cutoff_floor')::timestamptz, '-infinity'::timestamptz),
                            COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz))
       AND b.started_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY b.started_at DESC
     LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
    -- The whole cluster's: any task with no audit row, its guests' status
    -- changes and parts of backup jobs, and the runs. A task names a guest only
    -- when it is a guest task (is_guest_upid).
    UNION ALL
    (SELECT ('status:' || s.id::text)::text, 'status', s.id, s.cluster_id, s.observed_at, 'detected', 'status_change',
            'status', '', s.vmid, NULL::int, NULL::uuid, '', s.node, '', s.from_status, s.to_status, s.last_seen_at, 0,
            'cluster_status'::text
     FROM guest_status_changes s
     WHERE sqlc.arg('whole_cluster')::bool
       AND (sqlc.narg('source')::text IS NULL OR sqlc.narg('source')::text = 'detected')
       AND s.cluster_id = sqlc.arg('cluster_id')::uuid
       AND s.explained = false
       AND s.guest_type = ANY(sqlc.arg('status_guest_types')::text[])
       AND s.observed_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
       AND s.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY s.observed_at DESC
     LIMIT CASE WHEN sqlc.arg('whole_cluster')::bool THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
    UNION ALL
    (SELECT ('backup:' || b.upid || ':' || b.vmid::text)::text, 'backup', NULL::uuid, b.cluster_id, b.started_at, b.source,
            'vzdump', 'backup', '', b.vmid, NULL::int, b.user_id, b.upid, b.node, '', '', '', NULL, 0,
            'cluster_backups'::text
     FROM guest_backups b
     WHERE sqlc.arg('whole_cluster')::bool AND sqlc.arg('include_tasks')::bool
       AND (sqlc.narg('source')::text IS NULL OR b.source = sqlc.narg('source')::text)
       AND b.cluster_id = sqlc.arg('cluster_id')::uuid
       AND b.started_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
       AND b.started_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
       AND (sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
            OR EXISTS (SELECT 1 FROM vms v WHERE v.cluster_id = b.cluster_id AND v.vmid = b.vmid
                       AND v.type = ANY(sqlc.arg('status_guest_types')::text[])))
     ORDER BY b.started_at DESC
     LIMIT CASE WHEN sqlc.arg('whole_cluster')::bool THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
    -- What Nexara saw change in Proxmox that nothing on record explains
    -- (cluster_object_changes, explained = false), of the types the caller may
    -- view (observed_types, permittedObservedTypes): the guests' by vmid, from
    -- the floor; the objects' by id, by type (a whole type) or by the node a
    -- node's part names; the whole cluster's. A row naming no cluster is a
    -- global kind's alone (000125): every other read names its cluster.
    UNION ALL
    (SELECT ('observed:' || o.id::text)::text, 'observed', o.id, o.cluster_id, o.observed_at, 'detected', o.change,
            o.resource_type, o.resource_id, o.vmid, o.vmid, NULL::uuid, '', o.node, '', '', '', o.last_seen_at, 0,
            'guest_observed'::text
     FROM cluster_object_changes o
     WHERE o.cluster_id = sqlc.arg('cluster_id')::uuid
       AND (sqlc.narg('source')::text IS NULL OR sqlc.narg('source')::text = 'detected')
       AND o.vmid IS NOT NULL
       AND o.vmid = ANY(sqlc.arg('vmids')::int[])
       AND o.resource_type = ANY(sqlc.arg('observed_types')::text[])
       AND o.explained = false
       AND history_worker_ok(NULL, sqlc.arg('guest_workers')::text[])
       AND o.observed_at >= GREATEST(COALESCE(sqlc.narg('cutoff_floor')::timestamptz, '-infinity'::timestamptz),
                            COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz))
       AND o.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY o.observed_at DESC
     LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
    UNION ALL
    (SELECT ('observed:' || x.id::text)::text, 'observed', x.id, x.cluster_id, x.observed_at, 'detected', x.change,
            x.resource_type, x.resource_id, 0, NULL::int, NULL::uuid, '', x.node, '', '', '', x.last_seen_at, 0,
            'objects_observed'::text
     FROM (SELECT DISTINCT ON (m.id) m.*
           FROM objects ob
           CROSS JOIN LATERAL (
               (SELECT o.id, o.cluster_id, o.observed_at, o.change, o.resource_type, o.resource_id, o.node,
                       o.last_seen_at
                FROM cluster_object_changes o
                WHERE ob.detail_key = '' AND NOT ob.whole
                  AND o.resource_type = ob.resource_type AND o.resource_id = ob.value
                  AND (sqlc.arg('global')::bool AND o.cluster_id IS NULL
                       OR NOT sqlc.arg('global')::bool AND o.cluster_id = sqlc.arg('cluster_id')::uuid)
                  AND o.vmid IS NULL
                  AND o.explained = false
                  AND o.observed_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
                  AND o.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
                ORDER BY o.observed_at DESC
                LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
               UNION ALL
               (SELECT o.id, o.cluster_id, o.observed_at, o.change, o.resource_type, o.resource_id, o.node,
                       o.last_seen_at
                FROM cluster_object_changes o
                WHERE ob.whole
                  AND o.resource_type = ob.resource_type
                  AND (sqlc.arg('global')::bool AND o.cluster_id IS NULL
                       OR NOT sqlc.arg('global')::bool AND o.cluster_id = sqlc.arg('cluster_id')::uuid)
                  AND o.vmid IS NULL
                  AND o.explained = false
                  AND o.observed_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
                  AND o.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
                ORDER BY o.observed_at DESC
                LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
               UNION ALL
               -- A node's part by the node it names: audit rows carry it in
               -- details.node, observed rows in their node column.
               (SELECT o.id, o.cluster_id, o.observed_at, o.change, o.resource_type, o.resource_id, o.node,
                       o.last_seen_at
                FROM cluster_object_changes o
                WHERE ob.detail_key = 'node' AND NOT ob.in_list AND ob.resource_id = ''
                  AND o.resource_type = ob.resource_type AND o.node = ob.value
                  AND NOT sqlc.arg('global')::bool AND o.cluster_id = sqlc.arg('cluster_id')::uuid
                  AND o.vmid IS NULL
                  AND o.explained = false
                  AND o.observed_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
                  AND o.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
                ORDER BY o.observed_at DESC
                LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
           ) m
           WHERE ob.resource_type = ANY(sqlc.arg('observed_types')::text[])
             AND (sqlc.narg('source')::text IS NULL OR sqlc.narg('source')::text = 'detected')
           ORDER BY m.id) x
     ORDER BY x.observed_at DESC
     LIMIT NULLIF(sqlc.arg('row_cap')::int, 0))
    UNION ALL
    (SELECT ('observed:' || o.id::text)::text, 'observed', o.id, o.cluster_id, o.observed_at, 'detected', o.change,
            o.resource_type, o.resource_id, COALESCE(o.vmid, 0), NULL::int, NULL::uuid, '', o.node, '', '', '',
            o.last_seen_at, 0, 'cluster_observed'::text
     FROM cluster_object_changes o
     WHERE sqlc.arg('whole_cluster')::bool
       AND (sqlc.narg('source')::text IS NULL OR sqlc.narg('source')::text = 'detected')
       AND o.cluster_id = sqlc.arg('cluster_id')::uuid
       AND o.resource_type = ANY(sqlc.arg('observed_types')::text[])
       AND o.explained = false
       AND o.observed_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
       AND o.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY o.observed_at DESC
     LIMIT CASE WHEN sqlc.arg('whole_cluster')::bool THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
    -- A node's: the tasks it ran with no audit row, its status changes, and
    -- the guests that arrived on it or left it.
    UNION ALL
    (SELECT ('nodestatus:' || n.id::text)::text, 'status', n.id, n.cluster_id, n.observed_at, 'detected', 'status_change',
           'node', n.node, 0, NULL::int, NULL::uuid, '', n.node, '', n.from_status, n.to_status, n.last_seen_at, 0,
           'node_status'::text
    FROM (SELECT x.id, x.cluster_id, x.node, x.from_status, x.to_status, x.last_seen_at, x.observed_at,
                 lead(x.observed_at) OVER w AS next_at, lead(x.to_status) OVER w AS next_to,
                 lag(x.observed_at) OVER w AS prev_at, lag(x.from_status) OVER w AS prev_from
          FROM node_status_changes x
          WHERE sqlc.arg('node_name')::text <> ''
            AND x.cluster_id = sqlc.arg('cluster_id')::uuid
            AND x.node = sqlc.arg('node_name')::text
            AND x.run_count = 0
            AND x.observed_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
                                 - interval '2 minutes'
            AND x.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz) + interval '2 minutes'
          WINDOW w AS (ORDER BY x.observed_at, x.id)) n
    WHERE n.observed_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
    AND n.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
      AND (sqlc.narg('source')::text IS NULL OR sqlc.narg('source')::text = 'detected')
      AND NOT (n.to_status = 'unknown'
               AND (n.next_at IS NULL AND n.observed_at > now() - interval '2 minutes'
                    OR COALESCE(n.next_to = n.from_status AND n.next_at < n.observed_at + interval '2 minutes', false)))
      AND NOT COALESCE(n.from_status = 'unknown' AND n.prev_from = n.to_status
                       AND n.observed_at < n.prev_at + interval '2 minutes', false)
     ORDER BY n.observed_at DESC
     LIMIT CASE WHEN sqlc.arg('node_name')::text <> '' THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
    -- The runs of status changes past the hourly cap, and of moves: each one
    -- entry, its first change as last_seen_at, as a cluster's runs are.
    UNION ALL
    (SELECT ('nodestatus:' || x.id::text)::text, 'status', x.id, x.cluster_id, x.observed_at, 'detected', 'status_change',
           'node', x.node, 0, NULL::int, NULL::uuid, '', x.node, '', x.from_status, x.to_status, x.last_seen_at,
           x.run_count, 'node_status_runs'::text
    FROM node_status_changes x
    WHERE sqlc.arg('node_name')::text <> ''
      AND (sqlc.narg('source')::text IS NULL OR sqlc.narg('source')::text = 'detected')
      AND x.cluster_id = sqlc.arg('cluster_id')::uuid
      AND x.node = sqlc.arg('node_name')::text
      AND x.run_count > 0
      AND x.observed_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
      AND x.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY x.observed_at DESC
     LIMIT CASE WHEN sqlc.arg('node_name')::text <> '' THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
    UNION ALL
    (SELECT ('move:' || g.id::text)::text, 'move', g.id, g.cluster_id, g.observed_at, 'detected', 'moved',
           CASE g.guest_type WHEN 'lxc' THEN 'container' ELSE 'vm' END, g.vmid::text, g.vmid, NULL::int, NULL::uuid,
           '', CASE WHEN g.to_node = sqlc.arg('node_name')::text THEN g.from_node ELSE g.to_node END,
           '', g.from_node, g.to_node, g.last_seen_at, g.run_count, 'move_runs'::text
    FROM guest_node_changes g
    WHERE sqlc.arg('node_name')::text <> ''
      AND (sqlc.narg('source')::text IS NULL OR sqlc.narg('source')::text = 'detected')
      AND g.cluster_id = sqlc.arg('cluster_id')::uuid
      AND sqlc.arg('node_name')::text = ANY(g.nodes)
      AND g.run_count > 0
      AND g.guest_type = ANY(sqlc.arg('status_guest_types')::text[])
      AND g.observed_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
      AND g.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
     ORDER BY g.observed_at DESC
     LIMIT CASE WHEN sqlc.arg('node_name')::text <> '' THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
    UNION ALL
    (SELECT ('move:' || g.id::text)::text, 'move', g.id, g.cluster_id, g.observed_at, 'detected',
           CASE WHEN NOT m.migrated THEN 'now_on'
                WHEN g.to_node = sqlc.arg('node_name')::text THEN 'arrived' ELSE 'left' END,
           CASE g.guest_type WHEN 'lxc' THEN 'container' ELSE 'vm' END, g.vmid::text, g.vmid, NULL::int, NULL::uuid,
           '', CASE WHEN g.to_node = sqlc.arg('node_name')::text THEN g.from_node ELSE g.to_node END,
           '', g.from_node, g.to_node, g.last_seen_at, 0, 'moves'::text
    FROM guest_node_changes g
    CROSS JOIN LATERAL (
        SELECT EXISTS (
            SELECT 1
            FROM audit_log a
            LEFT JOIN task_history_archive r ON r.upid = a.details->>'upid' AND r.cluster_id = a.cluster_id
            LEFT JOIN task_history t ON t.upid = a.details->>'upid' AND t.cluster_id = a.cluster_id
            WHERE a.cluster_id = g.cluster_id AND a.vmid IS NOT NULL AND a.vmid = g.vmid
              AND a.created_at BETWEEN g.last_seen_at - interval '1 day' AND g.observed_at
              AND split_part(a.details->>'upid', ':', 6) IN ('qmigrate', 'vzmigrate', 'hamigrate',
                                                             'harelocate', 'qmtunnel', 'vzmtunnel')
              AND (COALESCE(r.status, t.status) = 'completed'
                   OR COALESCE(r.status, t.status) = 'stopped'
                      AND (upper(btrim(COALESCE(r.exit_status, t.exit_status))) IN ('', 'OK')
                           OR upper(btrim(COALESCE(r.exit_status, t.exit_status))) LIKE 'OK %'
                           OR upper(btrim(COALESCE(r.exit_status, t.exit_status))) LIKE 'WARNINGS%'))
              AND COALESCE(r.finished_at, t.finished_at)
                  BETWEEN g.last_seen_at - interval '5 minutes' AND g.observed_at + interval '5 minutes'
              AND COALESCE(a.details->>'target', a.details->>'target_node', g.to_node) = g.to_node)
        AS migrated) m
    WHERE sqlc.arg('node_name')::text <> ''
      AND (sqlc.narg('source')::text IS NULL OR sqlc.narg('source')::text = 'detected')
      AND g.cluster_id = sqlc.arg('cluster_id')::uuid
      AND (g.from_node = sqlc.arg('node_name')::text OR g.to_node = sqlc.arg('node_name')::text)
      AND g.run_count = 0
      AND g.guest_type = ANY(sqlc.arg('status_guest_types')::text[])
      AND g.observed_at >= COALESCE(sqlc.narg('since')::timestamptz, '-infinity'::timestamptz)
      AND g.observed_at < COALESCE(sqlc.narg('until')::timestamptz, 'infinity'::timestamptz)
      AND NOT EXISTS (SELECT 1
                      FROM (SELECT a.vmid, a.action, a.details FROM audit_log a
                            WHERE a.cluster_id = g.cluster_id
                              AND a.created_at BETWEEN GREATEST(g.last_seen_at, g.observed_at - interval '1 hour')
                                                   AND g.observed_at
                            OFFSET 0) a
                      WHERE a.vmid = g.vmid
                            AND split_part(a.details->>'upid', ':', 6) IN ('qmcreate', 'vzcreate', 'qmrestore',
                                                                           'vzrestore', 'qmdestroy', 'vzdestroy')
                         OR a.action IN ('clone', 'clone-to-template')
                            AND a.details->>'new_id' = g.vmid::text)
     ORDER BY g.observed_at DESC
     LIMIT CASE WHEN sqlc.arg('node_name')::text <> '' THEN NULLIF(sqlc.arg('row_cap')::int, 0) ELSE 0 END)
), horizon AS MATERIALIZED (
    -- How far back a capped read is whole: the latest of the points where a
    -- source that reached its cap stopped — each such source's oldest row
    -- read — and where the guests' floor cut their rows. NULL when no source
    -- reached its cap, or none is set. A source's rows are counted where its
    -- cap was applied: the audit arms in audit, the tasks before the probe
    -- for their audit row, each kind of run before it is folded into runs.
    SELECT (max(h.at) + interval '1 microsecond')::timestamptz AS at
    FROM (SELECT min(a.created_at) AS at FROM audit a GROUP BY a.src HAVING count(*) >= NULLIF(sqlc.arg('row_cap')::int, 0)
          UNION ALL
          SELECT f.at FROM vmid_floor f
          UNION ALL
          SELECT min(r.started_at) FROM tasks_raw r GROUP BY r.src HAVING count(*) >= NULLIF(sqlc.arg('row_cap')::int, 0)
          UNION ALL
          SELECT min(r.created_at) FROM run_rows r GROUP BY r.resource_type, r.action HAVING count(*) >= NULLIF(sqlc.arg('row_cap')::int, 0)
          UNION ALL
          SELECT min(e.occurred_at) FROM entries_core e
          WHERE e.src NOT IN ('vmid', 'clone', 'ha', 'objects', 'cluster', 'task')
          GROUP BY e.src HAVING count(*) >= NULLIF(sqlc.arg('row_cap')::int, 0)) h
), runs AS (
    SELECT (array_agg(r.id ORDER BY r.created_at DESC, r.id DESC))[1]          AS last_id,
           r.cluster_id, r.resource_type, r.action,
           (array_agg(r.resource_id ORDER BY r.created_at DESC, r.id DESC))[1] AS resource_id,
           (array_agg(r.source ORDER BY r.created_at DESC, r.id DESC))[1]      AS source,
           (array_agg(r.user_id ORDER BY r.created_at DESC, r.id DESC))[1]     AS user_id,
           min(r.created_at) AS first_at, max(r.created_at) AS last_at, count(*)::int AS n
    FROM (SELECT x.*, sum(x.starts) OVER (PARTITION BY x.resource_type, x.action
                                          ORDER BY x.created_at, x.id) AS island
          FROM run_rows x) r
    -- Only the rows the read is whole for: the older ones are the next
    -- stretch's (end_time), and a burst across the point is one run in each.
    WHERE r.created_at >= COALESCE((SELECT h.at FROM horizon h), '-infinity'::timestamptz)
    GROUP BY r.cluster_id, r.resource_type, r.action, r.island
), entries AS MATERIALIZED (
    SELECT * FROM entries_core
    UNION ALL
    SELECT ('run:' || u.last_id::text)::text, 'run', u.last_id, u.cluster_id, u.last_at, u.source, u.action,
           u.resource_type, u.resource_id, 0, NULL::int, u.user_id, '', '', '', '', '', u.first_at, u.n, 'run'::text
    FROM runs u
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
    SELECT c.*, count(*) OVER () AS total,
           h.at AS searched_from
    FROM categorized c
    CROSS JOIN guest_cutoffs gc
    LEFT JOIN horizon h ON true -- one row either way: so sqlc reads it as nullable
    WHERE (c.subject IS NULL OR NOT gc.m ? c.subject::text
           OR c.occurred_at > (gc.m -> c.subject::text ->> 'after')::timestamptz
           OR ((gc.m -> c.subject::text ->> 'inclusive')::bool
               AND c.occurred_at = (gc.m -> c.subject::text ->> 'after')::timestamptz))
      AND (sqlc.arg('include_guests')::bool OR c.vmid = 0 OR c.kind = 'move')
      AND (c.vmid = 0 OR gc.m ? c.subject::text OR gc.m ? c.vmid::text
           OR sqlc.arg('status_guest_types')::text[] @> '{qemu,lxc}'
           OR EXISTS (SELECT 1 FROM vms v
                      WHERE v.cluster_id = c.cluster_id AND v.vmid = c.vmid
                        AND v.type = ANY(sqlc.arg('status_guest_types')::text[])))
      AND (sqlc.narg('since')::timestamptz IS NULL OR c.occurred_at >= sqlc.narg('since')::timestamptz)
      AND (sqlc.narg('until')::timestamptz IS NULL OR c.occurred_at < sqlc.narg('until')::timestamptz)
      AND (h.at IS NULL OR c.occurred_at >= h.at)
      AND (sqlc.narg('category')::text IS NULL OR c.category = sqlc.narg('category')::text)
      AND NOT COALESCE(c.category = ANY(sqlc.arg('exclude_categories')::text[]), false)
      AND (sqlc.narg('source')::text IS NULL OR c.source = sqlc.narg('source')::text)
      -- A Proxmox row is its Nexara twin's only while the twin is on the page.
      AND NOT (c.source = 'proxmox' AND c.kind = 'audit' AND c.upid <> '' AND c.upid IN (
            SELECT w.upid FROM categorized w
            WHERE w.kind = 'audit' AND w.source <> 'proxmox' AND w.upid <> ''
              AND (sqlc.narg('category')::text IS NULL OR w.category = sqlc.narg('category')::text)
              AND NOT COALESCE(w.category = ANY(sqlc.arg('exclude_categories')::text[]), false)
              AND (sqlc.narg('source')::text IS NULL OR w.source = sqlc.narg('source')::text)))
    ORDER BY c.occurred_at DESC, c.entry_id DESC
    LIMIT sqlc.arg('row_limit')::int OFFSET sqlc.arg('row_offset')::int
)
SELECT p.entry_id, p.kind, p.cluster_id, p.occurred_at, p.source, p.action, p.resource_type, p.resource_id, p.vmid,
       COALESCE(p.subject, 0)::int AS subject_vmid,
       p.user_id, p.upid, p.node, p.recorded_status, p.from_status, p.to_status, p.last_seen_at,
       p.run_count::int AS run_count, p.category, p.total,
       COALESCE(ad.details, oc.detail, '{}'::jsonb)           AS details,
       COALESCE(oc.fields, '{}'::text[])                      AS fields,
       COALESCE(u.email, '')::text                            AS user_email,
       COALESCE(u.display_name, '')::text                     AS user_display_name,
       COALESCE(gb.status, ar.status, th.status, '')::text               AS task_status,
       COALESCE(gb.exit_status, ar.exit_status, th.exit_status, '')::text AS task_exit_status,
       COALESCE(ar.description, th.description, '')::text                AS task_description,
       COALESCE(ar.task_type, th.task_type, '')::text                    AS task_type,
       COALESCE(ar.node, th.node, '')::text                              AS task_node,
       th.progress                                                       AS task_progress,
       COALESCE(ar.finished_at, th.finished_at)                          AS task_finished_at,
       gb.finished_at                                                    AS backup_finished_at,
       COALESCE(gb.log_start, 0)::int                                    AS log_start,
       COALESCE(gb.log_end, 0)::int                                      AS log_end,
       p.searched_from
FROM page p
LEFT JOIN audit_log ad
       ON p.kind IN ('audit', 'run') AND ad.id = p.ref_id
LEFT JOIN cluster_object_changes oc
       ON p.kind = 'observed' AND oc.id = p.ref_id
LEFT JOIN task_history th
       ON p.upid <> '' AND th.upid = p.upid AND th.cluster_id = p.cluster_id
LEFT JOIN task_history_archive ar
       ON p.upid <> '' AND ar.upid = p.upid AND ar.cluster_id = p.cluster_id
LEFT JOIN guest_backups gb
       ON p.kind = 'backup' AND gb.upid = p.upid AND gb.vmid = p.vmid
          AND gb.cluster_id = p.cluster_id
LEFT JOIN users u ON u.id = p.user_id
-- An empty page of a capped read still says how far back it searched: one
-- row with no entry (entry_id ''), which the handler reads only for that.
UNION ALL
SELECT ''::text, ''::text, NULL::uuid, now(), ''::text, ''::text, ''::text, ''::text, 0, 0, NULL::uuid, ''::text,
       ''::text, ''::text, ''::text, ''::text, NULL::timestamptz, 0, ''::text, 0::bigint, '{}'::jsonb, '{}'::text[],
       ''::text, ''::text, ''::text, ''::text, ''::text, ''::text, ''::text, NULL::float8, NULL::timestamptz,
       NULL::timestamptz, 0, 0, h.at
FROM horizon h
WHERE h.at IS NOT NULL AND NOT EXISTS (SELECT 1 FROM page)
ORDER BY occurred_at DESC, entry_id DESC;

-- ListGuestTypes is the type of each of a History's guests the inventory
-- holds, for the handler to keep only the guests the caller may open — a VM
-- with view:vm, a container with view:container — when the guests are the
-- kind's own choice rather than the one the path names (viewableGuests).
-- name: ListGuestTypes :many
SELECT vmid, type
FROM vms
WHERE cluster_id = sqlc.arg('cluster_id')
  AND vmid = ANY(sqlc.arg('vmids')::int[]);

-- ListMostActiveGuests is the row_limit of vmids with the latest audit rows,
-- one probe each on (cluster_id, vmid, created_at): the members a folder's
-- History follows past its cap. A guest with none comes last; the lower VMID
-- first among equals.
-- name: ListMostActiveGuests :many
SELECT v.vmid::int AS vmid
FROM unnest(sqlc.arg('vmids')::int[]) AS v(vmid)
LEFT JOIN LATERAL (
    SELECT a.created_at
    FROM audit_log a
    WHERE a.cluster_id = sqlc.arg('cluster_id')::uuid
      AND a.vmid IS NOT NULL
      AND a.vmid = v.vmid
    ORDER BY a.created_at DESC
    LIMIT 1
) l ON true
ORDER BY l.created_at DESC NULLS LAST, v.vmid
LIMIT sqlc.arg('row_limit')::int;

-- ListHistoryAlsoChanged is, for one page of a History, what each of its audit
-- rows (audit_ids) covered in Proxmox without naming it (explained_unnamed,
-- 000124), object by object: only of the types the caller may view
-- (observed_types), in the History's own cluster, a row naming no cluster only
-- for a global kind. One row per audit row.
-- name: ListHistoryAlsoChanged :many
SELECT x.audit_id::uuid AS audit_id,
       jsonb_agg(jsonb_build_object('resource_type', x.resource_type, 'resource_id', x.resource_id,
                                    'node', x.node, 'fields', x.fields)
                 ORDER BY x.first_seen, x.resource_type, x.resource_id)::jsonb AS objects
FROM (SELECT u.key AS audit_id, c.resource_type, c.resource_id, c.node,
             array_agg(DISTINCT f.field ORDER BY f.field) AS fields, min(c.observed_at) AS first_seen
      FROM cluster_object_changes c
      CROSS JOIN LATERAL jsonb_each(c.explained_unnamed) AS u(key, value)
      CROSS JOIN LATERAL jsonb_array_elements_text(u.value) AS f(field)
      WHERE c.explained_unnamed ?| sqlc.arg('audit_ids')::text[]
        AND u.key = ANY(sqlc.arg('audit_ids')::text[])
        AND c.resource_type = ANY(sqlc.arg('observed_types')::text[])
        AND (sqlc.arg('global')::bool AND c.cluster_id IS NULL
             OR NOT sqlc.arg('global')::bool AND c.cluster_id = sqlc.arg('cluster_id')::uuid)
      GROUP BY u.key, c.resource_type, c.resource_id, c.node) x
GROUP BY x.audit_id;
