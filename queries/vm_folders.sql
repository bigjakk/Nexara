-- name: ListVMFoldersByCluster :many
SELECT id, cluster_id, parent_id, name, created_at, updated_at
FROM vm_folders
WHERE cluster_id = $1
ORDER BY name;

-- name: GetVMFolder :one
SELECT id, cluster_id, parent_id, name, created_at, updated_at
FROM vm_folders
WHERE id = $1;

-- name: CreateVMFolder :one
INSERT INTO vm_folders (cluster_id, parent_id, name)
VALUES ($1, $2, $3)
RETURNING id, cluster_id, parent_id, name, created_at, updated_at;

-- name: RenameVMFolder :one
UPDATE vm_folders
SET name = $2, updated_at = NOW()
WHERE id = $1
RETURNING id, cluster_id, parent_id, name, created_at, updated_at;

-- name: MoveVMFolder :one
UPDATE vm_folders
SET parent_id = $2, updated_at = NOW()
WHERE id = $1
RETURNING id, cluster_id, parent_id, name, created_at, updated_at;

-- name: DeleteVMFolder :exec
DELETE FROM vm_folders WHERE id = $1;

-- name: ListVMFolderMembershipsByCluster :many
-- Membership is keyed on the stable Proxmox identity (cluster_id, vmid); join
-- back to vms to return each VM's current internal id so the API contract (and
-- the frontend, which maps by vm.id) is unchanged. Memberships whose VMID isn't
-- currently present in vms are naturally dropped by the join.
SELECT v.id AS vm_id, m.folder_id, m.updated_at
FROM vm_folder_memberships m
JOIN vms v ON v.cluster_id = m.cluster_id AND v.vmid = m.vmid
WHERE m.cluster_id = $1;

-- AssignVMToFolder returns the folder the guest was filed in before, '' when
-- none, for the audit row to name (from_folder_id): the folder it left has no
-- other record of it. The subquery reads the table as it stood when the
-- statement began, so it sees the row this upsert replaces.
-- name: AssignVMToFolder :one
INSERT INTO vm_folder_memberships (cluster_id, vmid, folder_id)
VALUES ($1, $2, $3)
ON CONFLICT (cluster_id, vmid) DO UPDATE
SET folder_id = EXCLUDED.folder_id,
    updated_at = NOW()
RETURNING COALESCE((SELECT p.folder_id::text FROM vm_folder_memberships p
                    WHERE p.cluster_id = $1 AND p.vmid = $2), '')::text AS from_folder_id;

-- UnassignVMFromFolder returns the folder the guest left, likewise; no row
-- when it was in none.
-- name: UnassignVMFromFolder :one
DELETE FROM vm_folder_memberships WHERE cluster_id = $1 AND vmid = $2
RETURNING folder_id;
