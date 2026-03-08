-- Rolling update orchestrator tables
CREATE TABLE rolling_update_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'paused', 'completed', 'failed', 'cancelled')),
    parallelism INT NOT NULL DEFAULT 1 CHECK (parallelism >= 1),
    reboot_after_update BOOLEAN NOT NULL DEFAULT true,
    auto_restore_guests BOOLEAN NOT NULL DEFAULT true,
    package_excludes TEXT[] NOT NULL DEFAULT '{}',
    failure_reason TEXT NOT NULL DEFAULT '',
    created_by UUID NOT NULL REFERENCES users(id),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rolling_update_jobs_cluster ON rolling_update_jobs(cluster_id);
CREATE INDEX idx_rolling_update_jobs_status ON rolling_update_jobs(status);

CREATE TABLE rolling_update_nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL REFERENCES rolling_update_jobs(id) ON DELETE CASCADE,
    node_name TEXT NOT NULL,
    node_order INT NOT NULL DEFAULT 0,
    step TEXT NOT NULL DEFAULT 'pending'
        CHECK (step IN ('pending', 'draining', 'awaiting_upgrade', 'rebooting', 'health_check', 'restoring', 'completed', 'failed', 'skipped')),
    failure_reason TEXT NOT NULL DEFAULT '',
    packages_json JSONB NOT NULL DEFAULT '[]',
    guests_json JSONB NOT NULL DEFAULT '[]',
    drain_started_at TIMESTAMPTZ,
    drain_completed_at TIMESTAMPTZ,
    upgrade_confirmed_at TIMESTAMPTZ,
    reboot_started_at TIMESTAMPTZ,
    reboot_completed_at TIMESTAMPTZ,
    health_check_at TIMESTAMPTZ,
    restore_started_at TIMESTAMPTZ,
    restore_completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rolling_update_nodes_job ON rolling_update_nodes(job_id);
CREATE INDEX idx_rolling_update_nodes_step ON rolling_update_nodes(step);

-- RBAC permissions for rolling updates
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view', 'rolling_update', 'View rolling update jobs'),
    (gen_random_uuid(), 'manage', 'rolling_update', 'Create, start, cancel, and manage rolling update jobs');

-- Grant view to all built-in roles
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions WHERE action = 'view' AND resource = 'rolling_update';
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions WHERE action = 'view' AND resource = 'rolling_update';
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions WHERE action = 'view' AND resource = 'rolling_update';

-- Grant manage to Admin and Operator
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions WHERE action = 'manage' AND resource = 'rolling_update';
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions WHERE action = 'manage' AND resource = 'rolling_update';
