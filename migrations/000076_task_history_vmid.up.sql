-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Adds a nullable vmid column to task_history so task lists can filter by
-- guest server-side (folder detail view, future VM-scoped task views).
--
-- The value is the id field of the Proxmox UPID
-- (UPID:<node>:<pid>:<pstart>:<starttime>:<type>:<id>:<user@realm>:), which is
-- the VMID for guest tasks and a storage/other identifier (or empty) for
-- non-guest tasks — those stay NULL. New rows derive vmid the same way inside
-- the insert queries (queries/tasks.sql), so app code cannot forget it.
--
-- Idempotent and safe to re-run if interrupted: IF NOT EXISTS guards, and the
-- backfill only touches rows still NULL.

ALTER TABLE task_history ADD COLUMN IF NOT EXISTS vmid INTEGER;

-- {1,9} digits: Proxmox VMIDs top out at 999999999, and the bound keeps a
-- pathological all-numeric id from overflowing the ::int cast.
UPDATE task_history
SET vmid = split_part(upid, ':', 7)::int
WHERE vmid IS NULL
  AND split_part(upid, ':', 7) ~ '^[0-9]{1,9}$';

CREATE INDEX IF NOT EXISTS idx_task_history_cluster_vmid
    ON task_history (cluster_id, vmid)
    WHERE vmid IS NOT NULL;
