-- name: GetTaskSyncState :one
SELECT last_synced_at FROM proxmox_task_sync_state WHERE cluster_id = $1;

-- name: UpsertTaskSyncState :exec
INSERT INTO proxmox_task_sync_state (cluster_id, last_synced_at)
VALUES ($1, $2)
ON CONFLICT (cluster_id) DO UPDATE SET last_synced_at = $2;

-- ListExistingTaskHistoryUPIDs and ListExistingAuditLogUPIDs are the batch
-- dedup the collector ingest uses: given a node's candidate UPIDs, return the
-- subset already recorded, so ingestTask skips them without a per-task SELECT
-- (the security review flagged the old 2×N point lookups). Together they cover
-- the original two dedup layers — task_history (Nexara-dispatched or
-- already-ingested external) and audit_log (any source: a UI action Nexara
-- already audited, or a legacy external task ingested before task_history rows
-- existed). Backed by idx_task_history_upid and idx_audit_log_upid.

-- name: ListExistingTaskHistoryUPIDs :many
SELECT upid FROM task_history WHERE upid = ANY(sqlc.arg('upids')::text[]);

-- ListExistingAuditLogUPIDs asks about ONE cluster's entries. Audit entries
-- outlive a removed cluster (under its old id, since 000122), and a cluster added
-- again has a new id but the same nodes and so the same UPIDs: matching the UPID
-- alone would call every task it ever ran "already recorded" and skip them for good.

-- name: ListExistingAuditLogUPIDs :many
SELECT (details->>'upid')::text AS upid FROM audit_log
WHERE cluster_id = sqlc.arg('cluster_id')::uuid AND details->>'upid' = ANY(sqlc.arg('upids')::text[]);

-- ListCrossClusterMigrationUPIDs is the third dedup layer, and unlike its two
-- siblings it is a CREDENTIAL boundary rather than a duplicate-row guard.
--
-- A cross-cluster migration hands Proxmox the target cluster's decrypted API
-- token in the `target-endpoint` property string, so the worker's die message
-- can echo it back. ingestTask writes PVE's status straight into
-- task_history.exit_status at INSERT — readable at view:task and, via the task
-- join, at view:audit — and 'qmigrate' is not in skipTaskTypes, so nothing else
-- keeps it out. Only internal/migration holds the secret and can scrub it.
--
-- The two sibling lookups do not cover this, because they answer "is the row
-- already recorded?" and the dangerous window is exactly the one where it is
-- NOT: startAndPollMigration writes the UPID onto the migration_jobs row
-- (SetMigrationJobStarted) and only then inserts the task_history and audit_log
-- rows. For the few statements in between, a sync tick listing this node finds
-- the task unrecorded and ingests it itself. This lookup catches it because
-- migration_jobs.upid is already set throughout that gap — the same ordering
-- ListRunningTaskHistoryByCluster's anti-join rests on. One documented
-- invariant, two guards, rather than two coincidences.
--
-- The two guards are NOT copies of each other and must stay individually
-- killable: the anti-join guards the reconciler's SELECT, this guards
-- ingestTask's INSERT. Different writes, different call paths, and the tests
-- cross-check that removing either leaves the other's test still passing.
--
-- `upid <> ''` because that is migration_jobs.upid's not-yet-dispatched
-- default; without it a job parked at pending would match a task row that
-- somehow carried an empty UPID.
-- name: ListCrossClusterMigrationUPIDs :many
SELECT upid FROM migration_jobs
WHERE migration_type = 'cross-cluster'
  AND upid <> ''
  AND upid = ANY(sqlc.arg('upids')::text[]);

-- An SDN apply (reloadnetworkall) reloads every node's network as a task of its
-- own (srvreload, service "networking"), which the collector ingests as a node
-- network apply made in Proxmox. They are parts of the apply, not applies. This
-- annotates them — details.parent_upid names the apply — and leaves the row's key
-- and action alone; the History renders "part of the SDN apply" from the
-- annotation.
--
-- Which reloads. pve-network's apply worker (API2/Network/SDN.pm) walks the node
-- list in order and, for each node, runs `pvesh set /nodes/<node>/network` and
-- waits for it: the reloads start one after another from the moment the apply
-- starts and all before it ends, and pvesh runs as root@pam whoever started the
-- apply. So a reload is linked only if the user in its UPID is root@pam and it
-- started within the apply's run — from its start (the apply starts them, a
-- reload cannot come first) to its end, with 10 seconds' slack for the nodes'
-- clocks at the end. A reload by a person, or by a token, in the same minute is a
-- network apply of its own and stays one.
--
-- The start is the one in the apply's UPID (its starttime, whole seconds on the
-- node), with 2 seconds' slack. The audit row's own time is not it: Nexara files
-- its row after Proxmox has answered, and the first node's reload is dated from
-- the node's clock to the second, so it can come before the row. A UPID that does
-- not carry a starttime falls back to the audit row's time. pstart, the field
-- before it, is nine digits once the node has been up some 497 days (pve-common
-- writes it with %08X from the process start in clock ticks).
--
-- When. Only once the apply's end is known (its task row is finished) or a
-- quarter of an hour has passed since it started, when the end is taken to be
-- then: the first ingest pass runs before the reconciler has finished the task,
-- and an apply still running would otherwise take reloads that are not its.
--
-- Only the Proxmox-sourced rows, and only once: a linked row carries the
-- annotation, and a later pass is called with the same rows. Called after every
-- ingest pass, so a node listed before the node that ran the apply is linked on a
-- later one.
-- name: LinkSDNApplyNodeTasks :execrows
WITH applies AS (
    SELECT u.upid, u.started_at,
           COALESCE(th.finished_at, u.started_at + interval '15 minutes') AS ended_at
    FROM (
        SELECT a.cluster_id, a.details->>'upid' AS upid,
               COALESCE(
                   CASE WHEN a.details->>'upid' ~ '^UPID:[^:]*:[0-9A-Fa-f]{8}:[0-9A-Fa-f]{8,9}:[0-9A-Fa-f]{8}:'
                        THEN to_timestamp(('x' || split_part(a.details->>'upid', ':', 5))::bit(32)::bigint) END,
                   a.created_at) AS started_at
        FROM audit_log a
        WHERE a.cluster_id = sqlc.arg('cluster_id')::uuid
          AND a.resource_type = 'sdn'
          AND a.resource_id = 'cluster'
          AND a.action = 'sdn_applied'
          AND a.details->>'upid' IS NOT NULL
          AND a.created_at > sqlc.arg('since')::timestamptz - interval '15 minutes'
    ) u
    LEFT JOIN task_history th ON th.cluster_id = u.cluster_id AND th.upid = u.upid
    WHERE th.finished_at IS NOT NULL OR u.started_at < now() - interval '15 minutes'
), parents AS (
    SELECT DISTINCT ON (c.id) c.id, p.upid
    FROM audit_log c
    JOIN applies p ON c.created_at >= p.started_at - interval '2 seconds' AND c.created_at <= p.ended_at + interval '10 seconds'
    WHERE c.cluster_id = sqlc.arg('cluster_id')::uuid
      AND c.resource_type = 'network'
      AND c.action = 'network_applied'
      AND c.source = 'proxmox'
      AND NOT (c.details ? 'parent_upid')
      AND split_part(c.details->>'upid', ':', 8) = 'root@pam'
      AND c.created_at > sqlc.arg('since')::timestamptz
    ORDER BY c.id, p.started_at DESC
)
UPDATE audit_log c
SET details = c.details || jsonb_build_object('parent_upid', m.upid)
FROM parents m
WHERE c.id = m.id;

-- Proxmox runs some actions as more than one task, each with a UPID of its own:
-- its task log lists them all, and so do Nexara's lists, which is where one action
-- reads as two or three. The two queries below record which rows belong together,
-- with keys in audit_log.details, and change nothing else. Every row stays listed;
-- the lists mark the linked ones (an "HA manager" or "Migration target" badge) so
-- a start by root@pam beside the operator's own reads as its second half and not
-- as a second action. Every key:
--
--   migration_upid     on a migration's start on its target node: the migration
--   target_start_upid  on the migration's row(s): that start
--   ha_request_upid    on an HA worker task: the request that caused it
--   ha_worker_upid     on an HA request's row(s): the task that did the work
--   ha_requested_by    on an HA worker whose request Nexara did not record: the
--                      PVE account that made it
--
-- Both read only rows the collector ingested (source 'proxmox') run by root@pam
-- itself, not by a token: the HA manager and a migration's own ssh run as that.
-- Types and times are the UPID's (fields 6 and 5), never the stored task_type,
-- which Nexara writes its own labels into. There is no migration for the rows
-- recorded before this: the collector links each cluster's history once per
-- start, in the background and a month at a time (backfillLinks).

-- A live migration starts the guest on the target node as a task of its own
-- (qmstart, vzstart), on real data within 5 seconds of the migration's start for
-- 99.2% of 6,028 and always before it ends. That start is linked to the migration
-- when it ran on another node than the migration and no earlier than it (2
-- seconds' slack for the nodes' clocks).
--
-- How late it may be depends on whether the migration's end is known. It is only
-- for one the collector ingested already finished (its row's recorded status is
-- the exit text, not RUNNING): finished_at is then Proxmox's own, and the start
-- must come before it. Every other finished_at is when a poll noticed — the
-- reconciler's up to a pass late, a day late for a task on a node that died —
-- so for those, as for a migration still running or with no task row, the start
-- must come within 5 seconds of the migration's. What that keeps out is a start
-- made after an offline migration: the second half of an HA relocation, or the
-- HA manager recovering a guest whose node died mid-migration. Each is an action
-- of its own, and marked as part of a migration it would read as nothing to look
-- at. What it leaves, at worst, is a slow migration's start unmarked, as before.
--
-- One start per migration, the earliest; a start in two migrations is the later
-- one's. Rows linked earlier are skipped, so a second call changes nothing.
-- name: LinkMigrationTargetStarts :execrows
WITH migrations AS (
    SELECT a.details->>'upid' AS upid, a.vmid,
           min(COALESCE(
               CASE WHEN a.details->>'upid' ~ '^UPID:[^:]*:[0-9A-Fa-f]{8}:[0-9A-Fa-f]{8,9}:[0-9A-Fa-f]{8}:'
                    THEN to_timestamp(('x' || split_part(a.details->>'upid', ':', 5))::bit(32)::bigint) END,
               a.created_at)) AS started,
           bool_or(a.source = 'proxmox'
                   AND upper(COALESCE(a.details->>'status', '')) NOT IN ('', 'RUNNING')) AS ingested_finished
    FROM audit_log a
    WHERE a.cluster_id = sqlc.arg('cluster_id')::uuid
      AND a.created_at > sqlc.arg('since')::timestamptz - interval '1 day'
      AND a.vmid IS NOT NULL
      AND split_part(a.details->>'upid', ':', 6) IN ('qmigrate', 'vzmigrate')
      AND NOT (a.details ? 'target_start_upid')
    GROUP BY a.details->>'upid', a.vmid
), timeline AS (
    -- One pass in time order per guest, a migration ahead of a start in its
    -- slack: every start then follows the migration it could belong to.
    SELECT m.vmid, split_part(m.upid, ':', 7) AS guest, m.started - interval '2 seconds' AS at, 0 AS follows,
           m.upid, NULL::uuid AS id, m.started,
           CASE WHEN m.ingested_finished THEN COALESCE(ar.finished_at, th.finished_at) END AS ended
    FROM migrations m
    LEFT JOIN task_history th ON th.upid = m.upid AND th.cluster_id = sqlc.arg('cluster_id')::uuid
    LEFT JOIN task_history_archive ar ON ar.upid = m.upid AND ar.cluster_id = sqlc.arg('cluster_id')::uuid
    UNION ALL
    SELECT c.vmid, split_part(c.details->>'upid', ':', 7), c.created_at, 1,
           c.details->>'upid', c.id, c.created_at, NULL::timestamptz
    FROM audit_log c
    WHERE c.cluster_id = sqlc.arg('cluster_id')::uuid
      AND c.created_at > sqlc.arg('since')::timestamptz
      AND c.source = 'proxmox'
      AND c.vmid IS NOT NULL
      AND split_part(c.details->>'upid', ':', 6) IN ('qmstart', 'vzstart')
      AND split_part(c.details->>'upid', ':', 8) = 'root@pam'
      AND NOT (c.details ?| ARRAY['migration_upid', 'ha_request_upid'])
), grouped AS (
    SELECT t.*, count(*) FILTER (WHERE t.follows = 0)
                    OVER (PARTITION BY t.vmid, t.guest ORDER BY t.at, t.follows, t.upid) AS span
    FROM timeline t
), headed AS (
    -- A span opens with its migration (none before the first: span 0).
    SELECT g.*, first_value(g.upid) OVER w AS migration_upid, first_value(g.started) OVER w AS migration_started,
           first_value(g.ended) OVER w AS migration_ended
    FROM grouped g
    WINDOW w AS (PARTITION BY g.vmid, g.guest, g.span ORDER BY g.at, g.follows, g.upid)
), pairs AS MATERIALIZED (
    SELECT DISTINCT ON (s.vmid, s.guest, s.span) s.id, s.upid, s.migration_upid
    FROM headed s
    WHERE s.follows = 1
      AND s.span > 0
      AND split_part(s.migration_upid, ':', 2) <> split_part(s.upid, ':', 2)
      AND (s.started < s.migration_ended
           OR s.migration_ended IS NULL AND s.started <= s.migration_started + interval '5 seconds')
    ORDER BY s.vmid, s.guest, s.span, s.at, s.id
), mark_migrations AS (
    UPDATE audit_log a
    SET details = a.details || jsonb_build_object('target_start_upid', p.upid)
    FROM pairs p
    WHERE a.cluster_id = sqlc.arg('cluster_id')::uuid
      AND a.details->>'upid' = p.migration_upid
)
UPDATE audit_log c
SET details = c.details || jsonb_build_object('migration_upid', p.migration_upid)
FROM pairs p
WHERE c.id = p.id;

-- A start, stop or migration of an HA-managed guest is two tasks: the request
-- (hastart, hastop, hamigrate, harelocate), run by whoever asked, which only
-- queues it with the HA manager and ends OK within a second whatever becomes of
-- it; and the task that does the work, which the HA manager runs as root@pam up
-- to half a minute later, on whichever node holds the guest. Measured over 2,332
-- requests: all but one had its worker, 1 to 30 seconds after it.
--
-- A worker is linked to the nearest request of its kind for the same guest that
-- started within the 2 minutes before it (2 seconds' slack after it), and that no
-- worker has taken; a request keeps one worker, its earliest. So a retry the HA
-- manager makes after a failed start stays a row of its own, and a worker that
-- lost its request to an earlier one looks again on the next call.
--
-- Who asked is the request row's own user. Where only the collector recorded
-- the request (made in Proxmox, or by a Nexara engine that files no row), that is
-- a PVE account, and the worker carries it too as ha_requested_by, for a list
-- that shows the worker without its request. A Nexara user's name is not copied:
-- it is the request row's to show, and gone with the account.
--
-- The worker's UPDATE is the statement's own, so the count returned is pairs.
-- name: LinkHAWorkerTasks :execrows
WITH requests AS (
    SELECT p.details->>'upid' AS upid, p.vmid,
           min(COALESCE(
               CASE WHEN p.details->>'upid' ~ '^UPID:[^:]*:[0-9A-Fa-f]{8}:[0-9A-Fa-f]{8,9}:[0-9A-Fa-f]{8}:'
                    THEN to_timestamp(('x' || split_part(p.details->>'upid', ':', 5))::bit(32)::bigint) END,
               p.created_at)) AS started,
           bool_or(p.source <> 'proxmox') AS recorded_by_nexara,
           min(p.details->>'proxmox_user') FILTER (WHERE p.source = 'proxmox') AS requester
    FROM audit_log p
    WHERE p.cluster_id = sqlc.arg('cluster_id')::uuid
      AND p.created_at > sqlc.arg('since')::timestamptz - interval '15 minutes'
      AND p.vmid IS NOT NULL
      AND split_part(p.details->>'upid', ':', 6) IN ('hastart', 'hastop', 'hamigrate', 'harelocate')
      AND NOT (p.details ? 'ha_worker_upid')
    GROUP BY p.details->>'upid', p.vmid
), timeline AS (
    -- One pass in time order per guest and action, a request ahead of a worker
    -- in its slack: every worker then follows the nearest request before it.
    SELECT r.vmid, split_part(r.upid, ':', 7) AS guest,
           CASE split_part(r.upid, ':', 6) WHEN 'hastart' THEN 'start' WHEN 'hastop' THEN 'stop' ELSE 'migrate' END AS action,
           r.started - interval '2 seconds' AS at, 0 AS follows,
           r.upid, NULL::uuid AS id, r.started, r.recorded_by_nexara, r.requester
    FROM requests r
    UNION ALL
    SELECT c.vmid, split_part(c.details->>'upid', ':', 7),
           CASE WHEN split_part(c.details->>'upid', ':', 6) IN ('qmstart', 'vzstart') THEN 'start'
                WHEN split_part(c.details->>'upid', ':', 6) IN ('qmigrate', 'vzmigrate') THEN 'migrate'
                ELSE 'stop' END,
           c.created_at, 1,
           c.details->>'upid', c.id, c.created_at, NULL::bool, NULL::text
    FROM audit_log c
    WHERE c.cluster_id = sqlc.arg('cluster_id')::uuid
      AND c.created_at > sqlc.arg('since')::timestamptz
      AND c.source = 'proxmox'
      AND c.vmid IS NOT NULL
      AND split_part(c.details->>'upid', ':', 6) IN ('qmstart', 'vzstart', 'qmshutdown', 'qmstop', 'vzshutdown',
                                                     'vzstop', 'qmigrate', 'vzmigrate')
      AND split_part(c.details->>'upid', ':', 8) = 'root@pam'
      AND NOT (c.details ?| ARRAY['migration_upid', 'ha_request_upid'])
), grouped AS (
    SELECT t.*, count(*) FILTER (WHERE t.follows = 0)
                    OVER (PARTITION BY t.vmid, t.guest, t.action ORDER BY t.at, t.follows, t.upid) AS span
    FROM timeline t
), headed AS (
    -- A span opens with its request (none before the first: span 0).
    SELECT g.*, first_value(g.upid) OVER w AS request_upid, first_value(g.started) OVER w AS request_started,
           first_value(g.recorded_by_nexara) OVER w AS request_by_nexara, first_value(g.requester) OVER w AS request_by
    FROM grouped g
    WINDOW w AS (PARTITION BY g.vmid, g.guest, g.action, g.span ORDER BY g.at, g.follows, g.upid)
), pairs AS MATERIALIZED (
    SELECT DISTINCT ON (w.vmid, w.guest, w.action, w.span) w.id, w.upid, w.request_upid,
           w.request_by_nexara AS recorded_by_nexara, w.request_by AS requester
    FROM headed w
    WHERE w.follows = 1
      AND w.span > 0
      AND w.started <= w.request_started + interval '2 minutes'
    ORDER BY w.vmid, w.guest, w.action, w.span, w.at, w.id
), mark_requests AS (
    UPDATE audit_log p
    SET details = p.details || jsonb_build_object('ha_worker_upid', m.upid)
    FROM pairs m
    WHERE p.cluster_id = sqlc.arg('cluster_id')::uuid
      AND p.details->>'upid' = m.request_upid
)
UPDATE audit_log c
SET details = c.details || jsonb_build_object('ha_request_upid', m.request_upid)
              || CASE WHEN NOT m.recorded_by_nexara AND m.requester IS NOT NULL
                      THEN jsonb_build_object('ha_requested_by', m.requester)
                      ELSE '{}'::jsonb END
FROM pairs m
WHERE c.id = m.id;
