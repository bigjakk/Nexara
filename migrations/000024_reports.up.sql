-- Report schedules
CREATE TABLE report_schedules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    report_type     TEXT NOT NULL CHECK (report_type IN ('resource_utilization', 'capacity_forecast', 'backup_compliance', 'patch_status', 'uptime_summary')),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    time_range_hours INT NOT NULL DEFAULT 168,
    schedule        TEXT NOT NULL DEFAULT '',
    format          TEXT NOT NULL DEFAULT 'html' CHECK (format IN ('html', 'csv')),
    email_enabled   BOOLEAN NOT NULL DEFAULT false,
    email_channel_id UUID REFERENCES notification_channels(id) ON DELETE SET NULL,
    email_recipients TEXT[] NOT NULL DEFAULT '{}',
    parameters      JSONB NOT NULL DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    last_run_at     TIMESTAMPTZ,
    next_run_at     TIMESTAMPTZ,
    created_by      UUID NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_report_schedules_cluster ON report_schedules(cluster_id);
CREATE INDEX idx_report_schedules_enabled ON report_schedules(enabled) WHERE enabled = true;
CREATE INDEX idx_report_schedules_next_run ON report_schedules(next_run_at) WHERE enabled = true;

-- Report runs (generated reports)
CREATE TABLE report_runs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_id     UUID REFERENCES report_schedules(id) ON DELETE SET NULL,
    report_type     TEXT NOT NULL,
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    time_range_hours INT NOT NULL DEFAULT 168,
    report_data     JSONB,
    report_html     TEXT,
    report_csv      TEXT,
    error_message   TEXT NOT NULL DEFAULT '',
    created_by      UUID NOT NULL REFERENCES users(id),
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_report_runs_schedule ON report_runs(schedule_id);
CREATE INDEX idx_report_runs_cluster ON report_runs(cluster_id);
CREATE INDEX idx_report_runs_created ON report_runs(created_at DESC);

-- RBAC permissions for reports
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view', 'report', 'View reports and report schedules'),
    (gen_random_uuid(), 'manage', 'report', 'Create, update, and delete report schedules'),
    (gen_random_uuid(), 'generate', 'report', 'Generate on-demand reports')
ON CONFLICT (action, resource) DO NOTHING;

-- Grant all report permissions to Admin role
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'report'
ON CONFLICT DO NOTHING;

-- Grant view + generate to Operator role
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'report' AND action IN ('view', 'generate')
ON CONFLICT DO NOTHING;

-- Grant view to Viewer role
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource = 'report' AND action = 'view'
ON CONFLICT DO NOTHING;
