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
SELECT m.vm_id, m.folder_id, m.updated_at
FROM vm_folder_memberships m
JOIN vm_folders f ON f.id = m.folder_id
WHERE f.cluster_id = $1;

-- name: GetVMFolderMembership :one
SELECT vm_id, folder_id, updated_at
FROM vm_folder_memberships
WHERE vm_id = $1;

-- name: AssignVMToFolder :exec
INSERT INTO vm_folder_memberships (vm_id, folder_id)
VALUES ($1, $2)
ON CONFLICT (vm_id) DO UPDATE
SET folder_id = EXCLUDED.folder_id,
    updated_at = NOW();

-- name: UnassignVMFromFolder :exec
DELETE FROM vm_folder_memberships WHERE vm_id = $1;
