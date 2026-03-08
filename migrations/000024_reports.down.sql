-- Remove role permission grants
DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE resource = 'report'
);

-- Remove permissions
DELETE FROM permissions WHERE resource = 'report';

-- Drop tables
DROP TABLE IF EXISTS report_runs;
DROP TABLE IF EXISTS report_schedules;
