-- Performance indexes for audit log query patterns.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_log_action_created ON audit_log (action, created_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_log_resource_type_created ON audit_log (resource_type, created_at DESC);
