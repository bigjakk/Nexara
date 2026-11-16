CREATE INDEX IF NOT EXISTS idx_audit_log_cluster_id ON audit_log (cluster_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_cluster_vmid
    ON audit_log (cluster_id, vmid)
    WHERE vmid IS NOT NULL;
