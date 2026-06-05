-- Revert vm_folder_memberships back to the vms.id surrogate key. Existing
-- assignments are re-derived from the current vms rows; assignments for VMIDs
-- not currently present in vms are dropped (they could not be expressed under
-- the surrogate key anyway).

-- 1. Re-add the surrogate column (nullable while we backfill).
ALTER TABLE vm_folder_memberships ADD COLUMN IF NOT EXISTS vm_id UUID;

-- 2. Resolve the surrogate from the natural key via the live vms rows.
UPDATE vm_folder_memberships m
SET vm_id = v.id
FROM vms v
WHERE v.cluster_id = m.cluster_id AND v.vmid = m.vmid;

-- 3. Drop memberships whose VMID no longer resolves to a vms row.
DELETE FROM vm_folder_memberships WHERE vm_id IS NULL;

-- 4. Restore the surrogate primary key and its ON DELETE CASCADE FK to vms.
ALTER TABLE vm_folder_memberships DROP CONSTRAINT vm_folder_memberships_cluster_fk;
ALTER TABLE vm_folder_memberships DROP CONSTRAINT vm_folder_memberships_pkey;
ALTER TABLE vm_folder_memberships ADD  CONSTRAINT vm_folder_memberships_pkey PRIMARY KEY (vm_id);
ALTER TABLE vm_folder_memberships
    ADD CONSTRAINT vm_folder_memberships_vm_id_fkey
    FOREIGN KEY (vm_id) REFERENCES vms(id) ON DELETE CASCADE;

-- 5. Drop the natural-key columns.
ALTER TABLE vm_folder_memberships DROP COLUMN cluster_id;
ALTER TABLE vm_folder_memberships DROP COLUMN vmid;
