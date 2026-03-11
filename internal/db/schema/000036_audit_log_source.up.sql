-- Add source column to audit_log to distinguish Nexara-initiated vs Proxmox-native actions.
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'nexara';
CREATE INDEX IF NOT EXISTS idx_audit_log_source ON audit_log (source);

-- Track high-water mark per cluster for incremental Proxmox task ingestion.
CREATE TABLE IF NOT EXISTS proxmox_task_sync_state (
    cluster_id UUID PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    last_synced_at BIGINT NOT NULL DEFAULT 0
);
