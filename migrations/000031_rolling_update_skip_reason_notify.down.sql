ALTER TABLE rolling_update_nodes DROP COLUMN IF EXISTS skip_reason;
ALTER TABLE rolling_update_jobs DROP COLUMN IF EXISTS notify_channel_id;
