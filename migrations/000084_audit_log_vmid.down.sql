DROP INDEX IF EXISTS idx_audit_log_cluster_vmid;
ALTER TABLE audit_log DROP COLUMN IF EXISTS vmid;
DROP FUNCTION IF EXISTS is_guest_upid(text);
