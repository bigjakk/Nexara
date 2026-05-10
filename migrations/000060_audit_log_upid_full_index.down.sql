-- Reverse 000060: restore the partial-by-source index and drop the full one.
CREATE INDEX IF NOT EXISTS idx_audit_log_proxmox_upid
    ON audit_log ((details->>'upid'))
    WHERE source = 'proxmox';
DROP INDEX IF EXISTS idx_audit_log_upid;
