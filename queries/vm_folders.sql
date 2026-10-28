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

-- LockClusterFolders takes the cluster's folder lock until the transaction
-- ends. Every move of a folder and filing of a guest takes it first, in a READ
-- COMMITTED transaction, so the statements after it read the tree as the last
-- one left it: without it two concurrent moves — A under B, B under A — each
-- pass their cycle check against the other's old tree and both commit, and two
-- concurrent filings of one guest record the same folder left. The two-key
-- form keeps its keys apart from the one-key locks elsewhere (0x4E58464C is
-- "NXFL").
-- name: LockClusterFolders :exec
SELECT pg_advisory_xact_lock(1314408012, hashtext(sqlc.arg('cluster_id')::uuid::text));

-- SetLocalLockTimeout bounds every lock wait of the rest of the transaction —
-- LockClusterFolders' among them — so requests queued behind a long change to
-- a cluster's tree give their connections back instead of holding the pool.
-- name: SetLocalLockTimeout :exec
SELECT set_config('lock_timeout', sqlc.arg('timeout')::text, true);

-- MoveVMFolder moves the folder under parent_id (null for the top level),
-- unless that is the folder itself or below it, or the tree above it runs past
-- 1,024 levels (handlers.folderDepthCap): then no row. Run under
-- LockClusterFolders.
-- name: MoveVMFolder :one
WITH RECURSIVE up AS (
    SELECT p.id, p.parent_id, 1 AS depth FROM vm_folders p WHERE p.id = sqlc.narg('parent_id')
    UNION ALL
    SELECT p.id, p.parent_id, up.depth + 1
    FROM vm_folders p JOIN up ON p.id = up.parent_id
    WHERE up.depth <= 1024
)
UPDATE vm_folders f
SET parent_id = sqlc.narg('parent_id'), updated_at = NOW()
WHERE f.id = sqlc.arg('id')
  AND NOT EXISTS (SELECT 1 FROM up WHERE up.id = f.id OR up.depth > 1024)
RETURNING f.id, f.cluster_id, f.parent_id, f.name, f.created_at, f.updated_at;

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
-- statement began, so it sees the row this upsert replaces — under
-- LockClusterFolders, which it runs under, the row the last filing left; begun
-- beside a concurrent filing, it would see the one before that.
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

-- ListVMFolderMembers is every membership of the cluster, by VMID, for a
-- folder's History: unlike ListVMFolderMembershipsByCluster it keeps a member
-- the inventory no longer lists, whose history is still the folder's.
-- name: ListVMFolderMembers :many
SELECT vmid, folder_id FROM vm_folder_memberships
WHERE cluster_id = $1;

-- ListUnfiledGuests is the guests of the cluster filed in no folder — the
-- sidebar tree's Discovered — for its History.
-- name: ListUnfiledGuests :many
SELECT v.vmid FROM vms v
WHERE v.cluster_id = $1
  AND NOT EXISTS (
      SELECT 1 FROM vm_folder_memberships m
      WHERE m.cluster_id = v.cluster_id AND m.vmid = v.vmid);
