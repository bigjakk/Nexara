-- ⚠️ BREAKING UPGRADE — automated data backfill (no manual steps required)
-- This migration re-keys vm_folder_memberships and backfills existing rows in
-- place. Existing folder assignments are preserved automatically; the procedure
-- and its recovery semantics are documented inline below.
--
-- WHY ----------------------------------------------------------------------
-- Folder membership was keyed to the ephemeral surrogate vms.id with an
-- ON DELETE CASCADE to vms. The collector prunes and re-inserts a vms row
-- whenever Proxmox transiently stops listing a guest on a synced node — most
-- often the brief window during a live migration (DRS, rolling-update drains,
-- HA failover, or PVE 9.2 native CRS). The re-insert draws a fresh
-- gen_random_uuid(), and the cascade silently dropped the membership, so the
-- VM "fell back to Discovered" on its own with no user action.
--
-- The fix keys membership on the stable Proxmox identity (cluster_id, vmid) —
-- the same identity Proxmox itself uses, mirroring how task_history keys on
-- upid — AND decouples membership lifetime from the churn-prone vms row by
-- removing the cascade to vms (cleanup now rides on the cluster, which the
-- collector never churns). Folder assignments now survive vms-row churn.
--
-- BEHAVIORAL CHANGE --------------------------------------------------------
-- Because membership now tracks (cluster_id, vmid), destroying a guest and
-- later creating a guest that reuses the same VMID inherits the old folder —
-- the inventory "slot" keeps its place, vCenter-style. This is intentional.
--
-- RECOVERY -----------------------------------------------------------------
-- The whole migration runs in one transaction (golang-migrate default). If it
-- fails or is interrupted it rolls back to the surrogate-keyed schema with no
-- data loss; re-running it re-attempts the backfill cleanly.

-- 1. Add the natural-key columns (nullable while we backfill).
ALTER TABLE vm_folder_memberships ADD COLUMN IF NOT EXISTS cluster_id UUID;
ALTER TABLE vm_folder_memberships ADD COLUMN IF NOT EXISTS vmid       INT;

-- 2. Backfill from the live vms rows the surrogate currently points at.
UPDATE vm_folder_memberships m
SET cluster_id = v.cluster_id,
    vmid       = v.vmid
FROM vms v
WHERE v.id = m.vm_id;

-- 3. Drop any memberships whose vm_id no longer resolves (already orphaned —
--    these would not have rendered in any folder anyway).
DELETE FROM vm_folder_memberships WHERE cluster_id IS NULL OR vmid IS NULL;

-- 4. Enforce NOT NULL now that every surviving row is backfilled.
ALTER TABLE vm_folder_memberships ALTER COLUMN cluster_id SET NOT NULL;
ALTER TABLE vm_folder_memberships ALTER COLUMN vmid       SET NOT NULL;

-- 5. Swap the primary key from the surrogate to the natural key. Dropping the
--    vm_id column below also drops its ON DELETE CASCADE FK to vms.
ALTER TABLE vm_folder_memberships DROP CONSTRAINT vm_folder_memberships_pkey;
ALTER TABLE vm_folder_memberships ADD  CONSTRAINT vm_folder_memberships_pkey PRIMARY KEY (cluster_id, vmid);

-- 6. Tie membership lifetime to the cluster (never churned by the collector),
--    not to the per-sync vms row. Deleting a cluster still cascades cleanup.
ALTER TABLE vm_folder_memberships
    ADD CONSTRAINT vm_folder_memberships_cluster_fk
    FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE;

-- 7. Drop the surrogate column (removing the old cascade to vms with it).
ALTER TABLE vm_folder_memberships DROP COLUMN vm_id;
