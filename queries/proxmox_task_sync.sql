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

-- name: ListExistingAuditLogUPIDs :many
SELECT (details->>'upid')::text AS upid FROM audit_log WHERE details->>'upid' = ANY(sqlc.arg('upids')::text[]);

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
-- clocks. A reload by a person, or by a token, in the same minute is a network
-- apply of its own and stays one.
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
    SELECT a.details->>'upid' AS upid,
           a.created_at AS started_at,
           COALESCE(th.finished_at, a.created_at + interval '15 minutes') AS ended_at
    FROM audit_log a
    LEFT JOIN task_history th ON th.cluster_id = a.cluster_id AND th.upid = a.details->>'upid'
    WHERE a.cluster_id = sqlc.arg('cluster_id')::uuid
      AND a.resource_type = 'sdn'
      AND a.resource_id = 'cluster'
      AND a.action = 'sdn_applied'
      AND a.details->>'upid' IS NOT NULL
      AND a.created_at > sqlc.arg('since')::timestamptz - interval '15 minutes'
      AND (th.finished_at IS NOT NULL OR a.created_at < now() - interval '15 minutes')
), parents AS (
    SELECT DISTINCT ON (c.id) c.id, p.upid
    FROM audit_log c
    JOIN applies p ON c.created_at >= p.started_at AND c.created_at <= p.ended_at + interval '10 seconds'
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
