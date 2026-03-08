-- Remove RBAC permissions
DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE resource = 'rolling_update'
);
DELETE FROM permissions WHERE resource = 'rolling_update';

-- Drop tables
DROP TABLE IF EXISTS rolling_update_nodes;
DROP TABLE IF EXISTS rolling_update_jobs;
