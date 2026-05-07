-- Notification dead-letter queue.
--
-- A row is written here when a dispatcher exhausts its retry budget without
-- success, OR when a per-channel rate-limit blocks a dispatch. The row
-- preserves enough information to (a) surface the failure in the UI for
-- operator review and (b) replay the dispatch on demand without needing the
-- original alert row to still exist.
--
-- channel_id and alert_id are SET NULL on cascade so the DLQ can outlive
-- the channel/alert that produced it. channel_type and channel_name are
-- denormalised at write time so a deleted channel is still identifiable.
-- The full AlertPayload is stored as JSONB in `payload` so the retry path
-- has everything needed to re-run the dispatcher without re-reading the
-- (possibly resolved or auto-resolved) alert.
CREATE TABLE IF NOT EXISTS notification_dlq (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id      UUID REFERENCES notification_channels(id) ON DELETE SET NULL,
    channel_type    TEXT NOT NULL,
    channel_name    TEXT NOT NULL DEFAULT '',
    alert_id        UUID REFERENCES alert_history(id) ON DELETE SET NULL,
    rule_id         UUID REFERENCES alert_rules(id) ON DELETE SET NULL,
    -- cluster_id is denormalised at insert time so the API layer can filter
    -- DLQ entries by the caller's accessible clusters even after the
    -- referencing alert/rule rows are deleted (both FKs above SET NULL on
    -- delete). Nullable because not every dispatch has a cluster (e.g. a
    -- rule-less test dispatch invoked from the channel-test endpoint, or a
    -- global alert rule with no cluster_id).
    cluster_id      UUID REFERENCES clusters(id) ON DELETE SET NULL,
    payload         JSONB NOT NULL DEFAULT '{}',
    last_error      TEXT NOT NULL DEFAULT '',
    attempt_count   INT NOT NULL DEFAULT 0,
    state           TEXT NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending', 'rate_limited', 'retrying', 'resolved', 'dismissed')),
    failure_kind    TEXT NOT NULL DEFAULT 'send_failed'
                    CHECK (failure_kind IN ('send_failed', 'rate_limited', 'config_error')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_notification_dlq_state ON notification_dlq(state);
CREATE INDEX IF NOT EXISTS idx_notification_dlq_created ON notification_dlq(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notification_dlq_channel ON notification_dlq(channel_id);
CREATE INDEX IF NOT EXISTS idx_notification_dlq_cluster ON notification_dlq(cluster_id);

-- RBAC permissions. View is granted to anyone with view:notification_channel
-- (operators need to see what's failing); manage requires manage:notification_channel
-- (retry / dismiss are management actions on the channel's traffic).
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view',   'notification_dlq', 'View notification dead-letter queue entries'),
    (gen_random_uuid(), 'manage', 'notification_dlq', 'Retry or dismiss notification dead-letter queue entries')
ON CONFLICT (action, resource) DO NOTHING;

-- Admin gets both.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'notification_dlq'
ON CONFLICT DO NOTHING;

-- Operator gets view + manage (channels are an Operator concern).
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'notification_dlq'
ON CONFLICT DO NOTHING;

-- Viewer gets view only.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource = 'notification_dlq' AND action = 'view'
ON CONFLICT DO NOTHING;
