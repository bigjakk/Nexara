-- Both insert queries derive vmid from the UPID id field (field 7 of
-- UPID:node:pid:pstart:starttime:type:id:user@realm:) in SQL — the same
-- expression migration 000076 used to backfill — so no insert path can
-- forget it. Non-guest tasks (empty/non-numeric id) store NULL; the {1,9}
-- bound (VMIDs cap at 999999999) keeps a pathological all-numeric id from
-- overflowing the ::int cast.
-- name: InsertTaskHistory :one
INSERT INTO task_history (cluster_id, user_id, upid, description, status, node, task_type, vmid)
VALUES ($1, $2, $3, $4, $5, $6, $7,
    CASE WHEN split_part($3, ':', 7) ~ '^[0-9]{1,9}$'
         THEN split_part($3, ':', 7)::int END)
ON CONFLICT (upid) DO NOTHING
RETURNING *;

-- InsertExternalTaskHistory records a PVE-native (non-Nexara) task discovered by
-- the collector. Unlike InsertTaskHistory it sets started_at/finished_at/
-- exit_status explicitly, so a task already finished when first seen is stored
-- fully-formed (and a still-running one as status='running'). Attributed to the
-- system user; ON CONFLICT keeps it idempotent across sync ticks.
-- name: InsertExternalTaskHistory :exec
INSERT INTO task_history (
    cluster_id, user_id, upid, description, status, exit_status,
    node, task_type, started_at, finished_at, source, vmid
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'proxmox',
    CASE WHEN split_part($3, ':', 7) ~ '^[0-9]{1,9}$'
         THEN split_part($3, ':', 7)::int END)
ON CONFLICT (upid) DO NOTHING;

-- name: UpdateTaskHistory :exec
UPDATE task_history
SET status = $2, exit_status = $3, progress = $4, finished_at = $5
WHERE upid = $1;

-- name: GetTaskByUpid :one
SELECT * FROM task_history
WHERE upid = $1
LIMIT 1;

-- DeleteCompletedTasks removes terminal task_history rows whose finish (or
-- start, if never finalized) predates the caller-supplied cutoff. Never deletes
-- a still-running row — the status guard keeps long disk-moves/migrations in the
-- source of truth. Cutoff is computed in Go (now - retention) so the window is
-- configurable (TASK_HISTORY_RETENTION); shared by the automatic scheduler sweep
-- and the manual Clear-Completed endpoint.
-- name: DeleteCompletedTasks :exec
DELETE FROM task_history
WHERE status != 'running' AND COALESCE(finished_at, started_at) < sqlc.arg('cutoff')::timestamptz;

-- ListRunningTaskHistoryByCluster feeds the collector's reconciler
-- (internal/collector/task_reconcile.go): the still-running rows whose
-- running→done transition the collector owns.
--
-- The NOT EXISTS is a CREDENTIAL boundary. A cross-cluster migration hands
-- Proxmox the target cluster's decrypted API token inside the `target-endpoint`
-- property string, so the worker's die message can echo it back. The migration
-- orchestrator scrubs its copy (scrubEndpointSecret in
-- internal/migration/orchestrator.go); the collector cannot. It holds neither
-- the secret nor anything that identifies the task as a remote migration —
-- startAndPollMigration stores task_type 'migrate' for intra- and cross-cluster
-- alike. Both finalize with the same `upid = $1 AND status = 'running'`
-- predicate, so before this filter it was first-write-wins: a sync tick landing
-- in the orchestrator's 5s poll gap stored PVE's UNSCRUBBED text in exit_status
-- — readable at view:task and, via the task join, at view:audit — and the
-- scrubbed write then updated nothing.
--
-- One guard per write, and no copies: this covers the reconciler's SELECT,
-- ListCrossClusterMigrationUPIDs covers ingestTask's INSERT
-- (queries/proxmox_task_sync.sql). Different writes on different call paths, so
-- each stays individually killable — the tests cross-check that removing either
-- leaves the other's test passing. A second copy on THIS write would not be:
-- it would mask this one and neither could then be tested.
--
-- Keyed on migration_jobs.upid rather than a flag on task_history, so that a
-- future dispatch path gets the exclusion without having to remember it:
-- startAndPollMigration writes the UPID onto the job row before it inserts the
-- task row. That is an ORDERING, not an enforced invariant — the
-- SetMigrationJobStarted error is only logged, so a single failed UPDATE there
-- would leave migration_jobs.upid empty and this row visible again. Do not
-- read that as self-healing: pgxpool transparently replaces a dead connection,
-- so a conn-level failure on the UPDATE can be followed by a perfectly
-- successful InsertTaskHistory on a fresh one, leaving upid = '' beside a live
-- running row with both guards off. Only ctx cancellation reliably takes the
-- next statement down too.
--
-- The trade, stated plainly: an owner column on task_history WOULD be stronger
-- against that specific failure, because the flag and the row are one write.
-- It is weaker against the likelier one — every future insert site has to
-- remember to set it, which is the repo's "opt-in guard" class, silent and
-- permanent when forgotten. A failed UPDATE is at least loud. That is the
-- choice, not a claim that this dominates.
--
-- The `<> ''` keeps the two empty-string sentinels from joining to each other.
--
-- No index on migration_jobs(upid) on purpose: the table holds one row per
-- migration ever requested and is never pruned, and at that size the anti-join
-- is cheap under any plan. Revisit if it grows.
--
-- Narrow by design. Intra-cluster migrations stay in the set in every mode
-- (live, storage, both) — storage and both are migration_MODE values under the
-- intra-cluster type, and none of their text carries a credential.
--
-- What cross-cluster gives up: the collector's 24h stale-task sweep. Since
-- DeleteCompletedTasks never prunes a running row, anything this loop fails to
-- finalize shows "Running" forever, so pollTaskStatus was made to finalize on
-- every exit — on completion, on ctx.Done() (graceful shutdown), and after the
-- same 24h grace once Proxmox stops answering about the task at all, which is
-- the case staleTaskGrace existed for and needs no process death. Both added
-- exits write a constant, never vendor text.
--
-- One shape is still unbounded: a hard kill (SIGKILL, OOM, node loss) takes the
-- goroutine with no exit to run, and the row stays 'running'. Closing that needs
-- a startup or periodic sweep, which is a different mechanism from this filter —
-- and the same crash already strands migration_jobs in 'migrating', which
-- nothing reconciles either, so it is one gap rather than a new one.
--
-- 'cross-cluster' is migration.TypeCrossCluster, pinned by
-- TestGuard_RunningTaskListNamesTheCrossClusterType.
-- name: ListRunningTaskHistoryByCluster :many
SELECT th.* FROM task_history th
WHERE th.cluster_id = $1
  AND th.status = 'running'
  AND NOT EXISTS (
      SELECT 1 FROM migration_jobs mj
      WHERE mj.upid = th.upid
        AND mj.upid <> ''
        AND mj.migration_type = 'cross-cluster'
  );

-- ReconcileTaskHistory marks a still-running task terminal. Scoped to
-- status='running' so it never clobbers rows already finalized by the
-- migration orchestrator / DRS executor. :execrows lets the caller emit a
-- task_update event only when a row actually flipped.
--
-- Note what that guard does NOT do: it does not decide WHICH of two racing
-- writers wins, only that the loser is a no-op. Keeping an unscrubbed
-- exit_status out of the column is the job of the caller's read
-- (ListRunningTaskHistoryByCluster above), not of this predicate.
-- name: ReconcileTaskHistory :execrows
UPDATE task_history
SET status = $2, exit_status = $3, finished_at = $4, updated_at = now()
WHERE upid = $1 AND status = 'running';

-- ListTaskHistoryFiltered backs the Tasks page: optional cluster_id + status +
-- vmids filters with offset pagination. Mirrors ListAuditLogAdvanced. NULL
-- narg = no filter on that column. vmids matches the guest VMID parsed from
-- the UPID at insert (folder detail view passes a folder's VMID set).
-- accessible_cluster_ids carries the caller's view:task RBAC scope: NULL means
-- global access (no restriction); an array restricts rows — and the Total the
-- count query feeds into pagination — to those clusters ('{}' matches nothing).
--
-- sort_by/sort_dir drive server-side column sorting. The table is paginated at
-- 50 rows out of a history that runs to thousands, so the ordering has to be
-- applied to the whole filtered set — sorting the delivered page client-side
-- would only ever reshuffle the 50 rows already on screen. Both are validated
-- against a whitelist in the handler before they reach here; an unrecognised
-- value simply matches no CASE branch and falls through to the default order.
--
-- Sort keys order on the value the CELL RENDERS, not the raw column:
--   * blank text ('' node / task_type) sorts as absent, because the cell shows
--     an em dash for it;
--   * description falls back to the UPID, exactly as the cell does;
--   * cluster and vm sort by the joined NAME — the cell shows a name, and
--     ordering it by UUID or VMID would look arbitrary to the operator;
--   * status sorts by displayed severity (see sort_status), not alphabetically,
--     because 'stopped' renders as Completed or Failed depending on exit_status;
--   * progress sorts on the displayed fraction (see sort_progress).
--
-- Both LEFT JOINs are non-multiplying: clusters.id is a primary key and vms
-- carries UNIQUE (cluster_id, vmid), so neither can fan a task row out into
-- several. That is load-bearing — CountTaskHistoryFiltered does not join, and
-- a duplicated row here would make Items and Total disagree.
-- name: ListTaskHistoryFiltered :many
WITH scoped AS (
    SELECT t.*,
           c.name AS cluster_name,
           -- What the VM cell shows: the guest's name, or '#<vmid>' when the
           -- guest is gone (or was never one), or NULL for a non-guest task.
           COALESCE(NULLIF(v.name, ''), '#' || t.vmid::text) AS vm_label,
           -- Displayed severity, ordered so an ascending click surfaces
           -- failures first: 0 = Failed, 1 = Completed, 2 = Running.
           --
           -- The exit_status test mirrors Go's proxmox.TaskSucceeded and the
           -- frontend's isOkExit (components/layout/task-status.ts) — three
           -- copies of one rule, so a change to any of them has to change all
           -- three. Proxmox reports success as "OK", "OK (with warnings)" or
           -- "WARNINGS: N"; everything else is a failure.
           CASE
               WHEN t.status = 'running'   THEN 2
               WHEN t.status = 'completed' THEN 1
               WHEN t.status = 'failed'    THEN 0
               WHEN btrim(t.exit_status) = ''
                 OR upper(btrim(t.exit_status)) = 'OK'
                 OR upper(btrim(t.exit_status)) LIKE 'OK %'
                 OR upper(btrim(t.exit_status)) LIKE 'WARNINGS%' THEN 1
               ELSE 0
           END AS sort_status
    FROM task_history t
    LEFT JOIN clusters c ON c.id = t.cluster_id
    LEFT JOIN vms      v ON v.cluster_id = t.cluster_id AND v.vmid = t.vmid
    WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR t.cluster_id = sqlc.narg('cluster_id'))
      AND (sqlc.narg('status')::text   IS NULL OR t.status     = sqlc.narg('status'))
      AND (sqlc.narg('vmids')::int[]   IS NULL OR t.vmid       = ANY(sqlc.narg('vmids')::int[]))
      AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
           OR t.cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
), ranked AS (
    -- A second level so that each derived sort key is named once and both
    -- direction terms below can share it, without any of them reaching the
    -- result. (Not because ORDER BY cannot see them: it reads cluster_name,
    -- vm_label and sort_status straight off `scoped`. The restriction that
    -- does bite is narrower — sibling output columns cannot see each other
    -- within one SELECT list, and both keys here read a sibling of `scoped`:
    -- sort_progress reads sort_status, sort_text reads cluster_name and
    -- vm_label. Neither can move up a level.)
    --
    -- A row the UI draws as Completed shows a full bar whatever the stored
    -- progress is (Proxmox reports none for most task types), so it sorts as
    -- 1.0. Everything else sorts on the stored fraction, which stays NULL when
    -- Proxmox never reported one — the cell shows an indeterminate bar, and
    -- NULLS LAST keeps a screenful of "unknown" off the top of the table.
    --
    -- Ordering by progress is coarse on purpose, and today it is coarser than
    -- it looks: task_history.progress is only ever written by
    -- PUT /api/v1/tasks/:upid, which nothing currently calls, so the stored
    -- fraction is in practice always NULL. That leaves this key ordering
    -- displayed-Completed rows (1.0) against everything else (NULL, last).
    -- The live percentages an operator sees on running rows come from a
    -- per-row Proxmox poll in the browser that never reaches this table, so
    -- they cannot be ordered on here. If a reconciler ever starts persisting
    -- progress, this expression starts sorting properly with no change.
    SELECT scoped.*,
           CASE WHEN sort_status = 1 THEN 1.0 ELSE progress END AS sort_progress,
           -- Every text-valued key, resolved once. Which one is live depends on
           -- sort_by; the rest are simply not selected.
           CASE sqlc.arg('sort_by')::text
               WHEN 'cluster'     THEN cluster_name
               WHEN 'type'        THEN NULLIF(task_type, '')
               WHEN 'description' THEN COALESCE(NULLIF(description, ''), upid)
               WHEN 'vm'          THEN vm_label
               WHEN 'node'        THEN NULLIF(node, '')
           END AS sort_text
    FROM scoped
)
-- Only task_history's own columns are returned. cluster_name/vm_label/
-- sort_status/sort_progress/sort_text exist to be ordered on, and Postgres
-- keeps them visible to ORDER BY as columns of `ranked` without carrying them
-- into the result — which keeps the generated row struct the shape of the table.
SELECT id, cluster_id, user_id, upid, description, status, exit_status, node,
       task_type, progress, started_at, finished_at, created_at, updated_at,
       source, vmid
FROM ranked
ORDER BY
    -- One term per direction, each blanked when the other direction is active.
    -- A term that is NULL for every row ties, so control passes to the next.
    CASE WHEN sqlc.arg('sort_dir')::text = 'asc'  THEN sort_text END ASC NULLS LAST,
    CASE WHEN sqlc.arg('sort_dir')::text = 'desc' THEN sort_text END DESC NULLS LAST,
    CASE WHEN sqlc.arg('sort_by')::text = 'progress' AND sqlc.arg('sort_dir')::text = 'asc'
         THEN sort_progress END ASC NULLS LAST,
    CASE WHEN sqlc.arg('sort_by')::text = 'progress' AND sqlc.arg('sort_dir')::text = 'desc'
         THEN sort_progress END DESC NULLS LAST,
    CASE WHEN sqlc.arg('sort_by')::text = 'status' AND sqlc.arg('sort_dir')::text = 'asc'
         THEN sort_status END ASC,
    CASE WHEN sqlc.arg('sort_by')::text = 'status' AND sqlc.arg('sort_dir')::text = 'desc'
         THEN sort_status END DESC,
    CASE WHEN sqlc.arg('sort_by')::text = 'started' AND sqlc.arg('sort_dir')::text = 'asc'
         THEN started_at END ASC,
    -- The default order, and the tiebreaker under every other key. `id` makes
    -- the total order deterministic, which offset pagination depends on: rows
    -- tied on the sort key and left in whatever order the planner returned
    -- would drift between page 1 and page 2 and drop or repeat entries.
    started_at DESC, id
LIMIT $1 OFFSET $2;

-- CountTaskHistoryFiltered returns the total matching the same filters, for the
-- Tasks page pagination. Mirrors CountAuditLogAdvanced. Must stay filter-for-filter in
-- sync with ListTaskHistoryFiltered — in particular accessible_cluster_ids, or
-- the Total leaks other clusters' task counts to scoped users.
-- name: CountTaskHistoryFiltered :one
SELECT count(*) FROM task_history
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('status')::text   IS NULL OR status     = sqlc.narg('status'))
  AND (sqlc.narg('vmids')::int[]   IS NULL OR vmid       = ANY(sqlc.narg('vmids')::int[]))
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]));

-- name: GetClusterFailedTaskStats :one
-- pve_task_failed / pve_backup_failed: how many Proxmox tasks failed in a window.
--
-- "Failed" is task_history.status, which the collector derives from
-- proxmox.TaskSucceeded via classifyTaskExit — the single source of truth for
-- the success rule, shared with the DRS executor and both orchestrators.
-- Reusing it means this alert cannot disagree with what the Tasks page says
-- about the same UPID. It also settles the one genuinely ambiguous case:
-- "WARNINGS: N" is a SUCCESS there and so is not counted here. vzdump emits it
-- routinely, and a backup that warned still produced a backup.
--
-- Windowed on COALESCE(finished_at, started_at), i.e. when the task ENDED. A
-- backup that starts at 20:00 and fails at 04:00 failed an hour ago, not eight
-- — filtering on started_at would hide it from a six-hour rule and silently
-- shorten every failure's visible life by however long the task ran. The
-- COALESCE is belt-and-braces: every current writer of status='failed' sets
-- finished_at, and a row that somehow lacks one still gets counted rather than
-- vanishing.
--
-- exit_status 'vanished' is excluded. The collector writes it when a task has
-- been running past staleTaskGrace and Proxmox can no longer report on it
-- (see task_reconcile.go) — that is "we lost track of it", not "it failed", and
-- an alert that says a backup failed on that evidence is claiming more than it
-- knows.
--
-- @task_type '' matches any type; 'vzdump' narrows it to backups.
--
-- failed_count counts ROWS (three failures of one job are three failures), but
-- the name list is DISTINCT and ordered most-recent-first: a job that retried
-- five times would otherwise fill all three slots with its own name and hide
-- every other job that failed. unnamed_count is derived from the distinct count
-- so it stays consistent with the list it is qualifying.
WITH failed AS (
    SELECT description, COALESCE(finished_at, started_at) AS ended_at
    FROM task_history
    WHERE cluster_id = @cluster_id::uuid
      AND status = 'failed'
      AND exit_status <> 'vanished'
      AND COALESCE(finished_at, started_at) >= @since::timestamptz
      AND (@task_type::text = '' OR task_type = @task_type::text)
),
distinct_names AS (
    SELECT description, max(ended_at) AS last_failed
    FROM failed
    GROUP BY description
)
-- failed_names lists at most three and SAYS SO when it truncates, via
-- unnamed_count. A message reading "8 tasks failed: A, B, C" with no ellipsis
-- reads as the complete list, and an operator works three and never learns
-- about the other five.
SELECT
    (SELECT count(*) FROM failed)::bigint AS failed_count,
    COALESCE((SELECT string_agg(description, ', ' ORDER BY last_failed DESC)
              FROM (SELECT description, last_failed FROM distinct_names
                    ORDER BY last_failed DESC LIMIT 3) AS t), '')::text AS failed_names,
    GREATEST((SELECT count(*) FROM distinct_names) - 3, 0)::bigint AS unnamed_count;

-- ListTaskHistoryByTypeInWindow feeds the backup compliance report's "runs in
-- the period" section: every task of one worker type that ENDED inside the
-- window, newest first.
--
-- Windowed on COALESCE(finished_at, started_at) for the same reason
-- GetClusterFailedTaskStats is: a backup that starts before the window and
-- fails inside it belongs to this period's report. Still-running tasks have
-- no finished_at and are windowed on their start, so a run in flight at
-- generation time appears as running rather than vanishing.
--
-- exit_status 'vanished' rows are returned; the caller renders them as
-- "lost track of" rather than as failures.
-- name: ListTaskHistoryByTypeInWindow :many
SELECT * FROM task_history
WHERE cluster_id = @cluster_id::uuid
  AND task_type = @task_type::text
  AND COALESCE(finished_at, started_at) >= @since::timestamptz
  AND COALESCE(finished_at, started_at) < @until::timestamptz
ORDER BY started_at DESC
LIMIT @row_limit::int;

-- CountTaskHistoryByStatusInWindow feeds the cluster digest: how many tasks
-- ended in the period, by terminal status. Same window rule as the listing
-- above.
-- name: CountTaskHistoryByStatusInWindow :many
SELECT status, count(*)::bigint AS n
FROM task_history
WHERE cluster_id = @cluster_id::uuid
  AND COALESCE(finished_at, started_at) >= @since::timestamptz
  AND COALESCE(finished_at, started_at) < @until::timestamptz
GROUP BY status
ORDER BY status;

-- ListFailedTaskHistoryInWindow is the digest's failed-task list, newest
-- first. 'vanished' is excluded for the reason GetClusterFailedTaskStats
-- gives: losing track of a task is not the task failing.
-- name: ListFailedTaskHistoryInWindow :many
SELECT * FROM task_history
WHERE cluster_id = @cluster_id::uuid
  AND status = 'failed'
  AND exit_status <> 'vanished'
  AND COALESCE(finished_at, started_at) >= @since::timestamptz
  AND COALESCE(finished_at, started_at) < @until::timestamptz
ORDER BY COALESCE(finished_at, started_at) DESC
LIMIT @row_limit::int;
