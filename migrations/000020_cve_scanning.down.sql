-- Remove RBAC permission assignments
DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE resource = 'cve_scan'
);

-- Remove RBAC permissions
DELETE FROM permissions WHERE resource = 'cve_scan';

-- Drop tables in dependency order
DROP TABLE IF EXISTS cve_scan_vulns;
DROP TABLE IF EXISTS cve_scan_nodes;
DROP TABLE IF EXISTS cve_scans;
DROP TABLE IF EXISTS cve_cache;
