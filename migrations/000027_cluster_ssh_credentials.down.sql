ALTER TABLE rolling_update_nodes
    DROP COLUMN IF EXISTS upgrade_output,
    DROP COLUMN IF EXISTS upgrade_completed_at,
    DROP COLUMN IF EXISTS upgrade_started_at;

ALTER TABLE rolling_update_jobs
    DROP COLUMN IF EXISTS auto_upgrade;

ALTER TABLE rolling_update_nodes
    DROP CONSTRAINT IF EXISTS rolling_update_nodes_step_check;
ALTER TABLE rolling_update_nodes
    ADD CONSTRAINT rolling_update_nodes_step_check
        CHECK (step IN ('pending', 'draining', 'awaiting_upgrade', 'rebooting', 'health_check', 'restoring', 'completed', 'failed', 'skipped'));

DROP TABLE IF EXISTS cluster_ssh_credentials;

DELETE FROM role_permissions
WHERE permission_id IN (SELECT id FROM permissions WHERE action = 'manage' AND resource = 'ssh_credentials');

DELETE FROM permissions WHERE action = 'manage' AND resource = 'ssh_credentials';
