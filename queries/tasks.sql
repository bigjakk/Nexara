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

-- name: ListRunningTaskHistoryByCluster :many
SELECT * FROM task_history
WHERE cluster_id = $1 AND status = 'running';

-- ReconcileTaskHistory marks a still-running task terminal. Scoped to
-- status='running' so it never clobbers rows already finalized by the
-- migration orchestrator / DRS executor. :execrows lets the caller emit a
-- task_update event only when a row actually flipped.
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
