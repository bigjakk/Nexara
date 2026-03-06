-- 000014_audit_log_nullable_cluster.down.sql
-- Restore NOT NULL on cluster_id (delete rows without cluster first).

DELETE FROM audit_log WHERE cluster_id IS NULL;
ALTER TABLE audit_log ALTER COLUMN cluster_id SET NOT NULL;
