-- Revoke the permissions granted in the up migration first so dependent
-- role_permissions rows are gone before the permissions catalog is touched.
DELETE FROM role_permissions
WHERE permission_id IN (
    SELECT id FROM permissions
    WHERE (action, resource) IN (('view', 'vm_import'), ('manage', 'vm_import'))
);

DELETE FROM permissions
WHERE (action, resource) IN (('view', 'vm_import'), ('manage', 'vm_import'));

DROP TRIGGER IF EXISTS trg_vm_import_jobs_updated_at ON vm_import_jobs;
DROP INDEX IF EXISTS idx_vm_import_jobs_status;
DROP INDEX IF EXISTS idx_vm_import_jobs_cluster;
DROP TABLE IF EXISTS vm_import_jobs;
