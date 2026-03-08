-- Notification channels (schema for T1, delivery in T2)
CREATE TABLE notification_channels (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    channel_type    TEXT NOT NULL CHECK (channel_type IN ('email', 'webhook', 'slack', 'discord', 'pagerduty')),
    config_encrypted TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      UUID NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Alert rules
CREATE TABLE alert_rules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    severity        TEXT NOT NULL DEFAULT 'warning' CHECK (severity IN ('critical', 'warning', 'info')),
    metric          TEXT NOT NULL,
    operator        TEXT NOT NULL CHECK (operator IN ('>', '>=', '<', '<=', '==', '!=')),
    threshold       DOUBLE PRECISION NOT NULL,
    duration_seconds INT NOT NULL DEFAULT 300,
    scope_type      TEXT NOT NULL DEFAULT 'cluster' CHECK (scope_type IN ('cluster', 'node', 'vm')),
    cluster_id      UUID REFERENCES clusters(id) ON DELETE CASCADE,
    node_id         UUID REFERENCES nodes(id) ON DELETE CASCADE,
    vm_id           UUID REFERENCES vms(id) ON DELETE CASCADE,
    cooldown_seconds INT NOT NULL DEFAULT 3600,
    escalation_chain JSONB NOT NULL DEFAULT '[]',
    created_by      UUID NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_alert_rules_cluster ON alert_rules(cluster_id);
CREATE INDEX idx_alert_rules_enabled ON alert_rules(enabled) WHERE enabled = true;

-- Alert history (instances of fired alerts)
CREATE TABLE alert_history (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id         UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    state           TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'firing', 'acknowledged', 'resolved')),
    severity        TEXT NOT NULL DEFAULT 'warning',
    cluster_id      UUID REFERENCES clusters(id) ON DELETE CASCADE,
    node_id         UUID REFERENCES nodes(id) ON DELETE SET NULL,
    vm_id           UUID REFERENCES vms(id) ON DELETE SET NULL,
    resource_name   TEXT NOT NULL DEFAULT '',
    metric          TEXT NOT NULL,
    current_value   DOUBLE PRECISION NOT NULL,
    threshold       DOUBLE PRECISION NOT NULL,
    message         TEXT NOT NULL DEFAULT '',
    escalation_level INT NOT NULL DEFAULT 0,
    channel_id      UUID REFERENCES notification_channels(id) ON DELETE SET NULL,
    pending_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    fired_at        TIMESTAMPTZ,
    acknowledged_at TIMESTAMPTZ,
    acknowledged_by UUID REFERENCES users(id),
    resolved_at     TIMESTAMPTZ,
    resolved_by     UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_alert_history_rule ON alert_history(rule_id);
CREATE INDEX idx_alert_history_state ON alert_history(state);
CREATE INDEX idx_alert_history_cluster ON alert_history(cluster_id);
CREATE INDEX idx_alert_history_created ON alert_history(created_at DESC);

-- Maintenance windows
CREATE TABLE maintenance_windows (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    node_id         UUID REFERENCES nodes(id) ON DELETE CASCADE,
    description     TEXT NOT NULL DEFAULT '',
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ NOT NULL,
    created_by      UUID NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_window_valid CHECK (ends_at > starts_at)
);

CREATE INDEX idx_maintenance_windows_cluster ON maintenance_windows(cluster_id);
CREATE INDEX idx_maintenance_windows_active ON maintenance_windows(starts_at, ends_at);

-- RBAC permissions
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view', 'alert', 'View alerts and alert rules'),
    (gen_random_uuid(), 'manage', 'alert', 'Create, update, and delete alert rules'),
    (gen_random_uuid(), 'acknowledge', 'alert', 'Acknowledge and resolve alerts'),
    (gen_random_uuid(), 'view', 'notification_channel', 'View notification channels'),
    (gen_random_uuid(), 'manage', 'notification_channel', 'Manage notification channels'),
    (gen_random_uuid(), 'view', 'maintenance_window', 'View maintenance windows'),
    (gen_random_uuid(), 'manage', 'maintenance_window', 'Manage maintenance windows')
ON CONFLICT (action, resource) DO NOTHING;

-- Grant all alert permissions to Admin role
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource IN ('alert', 'notification_channel', 'maintenance_window')
ON CONFLICT DO NOTHING;

-- Grant view + acknowledge to Operator role
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE (resource IN ('alert', 'notification_channel', 'maintenance_window') AND action = 'view')
   OR (resource = 'alert' AND action = 'acknowledge')
ON CONFLICT DO NOTHING;

-- Grant view to Viewer role
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource IN ('alert', 'notification_channel', 'maintenance_window') AND action = 'view'
ON CONFLICT DO NOTHING;
