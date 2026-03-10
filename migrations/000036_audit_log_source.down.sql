DROP TABLE IF EXISTS proxmox_task_sync_state;
DROP INDEX IF EXISTS idx_audit_log_source;
ALTER TABLE audit_log DROP COLUMN IF EXISTS source;
