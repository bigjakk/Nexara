-- 000005_audit_log.up.sql
-- Audit log for tracking user actions on resources.

CREATE TABLE IF NOT EXISTS audit_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id    UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL,
    resource_id   TEXT NOT NULL,
    action        TEXT NOT NULL,
    details       JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_log_cluster_id ON audit_log (cluster_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_user_id ON audit_log (user_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log (created_at DESC);
