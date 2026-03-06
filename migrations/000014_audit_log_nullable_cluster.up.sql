-- 000014_audit_log_nullable_cluster.up.sql
-- Make cluster_id nullable to support system-wide audit events (auth, tasks)
-- that don't belong to any specific cluster.

ALTER TABLE audit_log ALTER COLUMN cluster_id DROP NOT NULL;
