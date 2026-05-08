-- 000058_drop_cve_notification_configs_channel_ids.down.sql
--
-- Rebuilds the channel_ids array from the join table, restoring the
-- pre-4.8c shape so a 4.8a-or-earlier binary can read it again. The
-- rebuild uses array_agg(channel_id) over cve_notification_config_channels
-- grouped by config_id; any config row with no children gets an empty
-- array via COALESCE. Run inside golang-migrate's per-file transaction
-- so a half-applied state can't leak.

ALTER TABLE cve_notification_configs
    ADD COLUMN IF NOT EXISTS channel_ids UUID[] NOT NULL DEFAULT ARRAY[]::UUID[];

UPDATE cve_notification_configs c
SET channel_ids = COALESCE(
    (
        SELECT array_agg(j.channel_id ORDER BY j.channel_id)
        FROM cve_notification_config_channels j
        WHERE j.config_id = c.cluster_id
    ),
    ARRAY[]::UUID[]
);
