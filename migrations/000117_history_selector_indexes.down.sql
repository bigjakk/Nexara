DROP FUNCTION IF EXISTS history_worker_ok(text, text[]);
-- Built after startup by db.EnsureHistoryIndexes, not by the up migration.
DROP INDEX IF EXISTS idx_audit_log_details;
DROP INDEX IF EXISTS idx_audit_log_resource_created;
