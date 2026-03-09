-- Add skip reason to node rows so users know why a node was skipped.
ALTER TABLE rolling_update_nodes
    ADD COLUMN IF NOT EXISTS skip_reason TEXT NOT NULL DEFAULT '';

-- Add optional notification channel for rolling update completion/failure alerts.
ALTER TABLE rolling_update_jobs
    ADD COLUMN IF NOT EXISTS notify_channel_id UUID REFERENCES notification_channels(id) ON DELETE SET NULL;
