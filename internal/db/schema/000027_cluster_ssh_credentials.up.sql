-- SSH credentials for automated apt dist-upgrade on Proxmox nodes.
CREATE TABLE cluster_ssh_credentials (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    username    TEXT NOT NULL DEFAULT 'root',
    port        INTEGER NOT NULL DEFAULT 22,
    auth_type   TEXT NOT NULL DEFAULT 'password'
        CHECK (auth_type IN ('password', 'key')),
    encrypted_password    TEXT NOT NULL DEFAULT '',
    encrypted_private_key TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id)
);

-- Add 'upgrading' step to rolling_update_nodes.
ALTER TABLE rolling_update_nodes
    DROP CONSTRAINT IF EXISTS rolling_update_nodes_step_check;
ALTER TABLE rolling_update_nodes
    ADD CONSTRAINT rolling_update_nodes_step_check
        CHECK (step IN ('pending', 'draining', 'awaiting_upgrade', 'upgrading', 'rebooting', 'health_check', 'restoring', 'completed', 'failed', 'skipped'));

-- Add auto_upgrade flag to rolling_update_jobs.
ALTER TABLE rolling_update_jobs
    ADD COLUMN auto_upgrade BOOLEAN NOT NULL DEFAULT false;

-- Add upgrade output capture to rolling_update_nodes.
ALTER TABLE rolling_update_nodes
    ADD COLUMN upgrade_started_at TIMESTAMPTZ,
    ADD COLUMN upgrade_completed_at TIMESTAMPTZ,
    ADD COLUMN upgrade_output TEXT NOT NULL DEFAULT '';

-- RBAC permissions for SSH credential management.
INSERT INTO permissions (id, action, resource, description)
VALUES
    (gen_random_uuid(), 'manage', 'ssh_credentials', 'Manage cluster SSH credentials')
ON CONFLICT DO NOTHING;

-- Grant to Admin role.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id
FROM permissions WHERE action = 'manage' AND resource = 'ssh_credentials'
ON CONFLICT DO NOTHING;
