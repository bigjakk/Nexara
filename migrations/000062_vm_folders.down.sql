-- Revoke the permissions granted in the up migration first so dependent
-- role_permissions rows are gone before the permissions catalog is touched.
DELETE FROM role_permissions
WHERE permission_id IN (
    SELECT id FROM permissions
    WHERE (action, resource) IN (('view', 'vm_folder'), ('manage', 'vm_folder'))
);

DELETE FROM permissions
WHERE (action, resource) IN (('view', 'vm_folder'), ('manage', 'vm_folder'));

DROP INDEX IF EXISTS idx_vm_folder_memberships_folder;
DROP TABLE IF EXISTS vm_folder_memberships;

DROP INDEX IF EXISTS idx_vm_folders_parent;
DROP INDEX IF EXISTS idx_vm_folders_cluster;
DROP TABLE IF EXISTS vm_folders;
