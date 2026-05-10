-- 000057_cve_notification_config_channels.up.sql
--
-- Phase 4.8a (additive, fully reversible). Introduce a join table that
-- replaces the cve_notification_configs.channel_ids UUID[] column. This
-- release is dual-write only: app code writes BOTH the array and the join
-- table on every upsert; reads still come from the array. A later release
-- (4.8b) flips reads to the join table; the data-loss release (4.8c)
-- drops the array column.
--
-- The backfill copies every (config, channel) pair already encoded in the
-- array into the new table on first apply, so existing installs come up
-- with both representations in agreement.

CREATE TABLE IF NOT EXISTS cve_notification_config_channels (
    config_id  UUID NOT NULL REFERENCES cve_notification_configs(cluster_id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    PRIMARY KEY (config_id, channel_id)
);

CREATE INDEX IF NOT EXISTS idx_cve_notification_config_channels_channel_id
    ON cve_notification_config_channels (channel_id);

-- Backfill from the array. unnest() of an empty array yields zero rows, so
-- configs with no channels stay empty in the join table — same shape.
-- ON CONFLICT keeps the migration re-runnable (e.g. if a prior partial
-- apply was rolled back; golang-migrate wraps each file in a transaction
-- so this is belt-and-braces, not a real recovery path).
INSERT INTO cve_notification_config_channels (config_id, channel_id)
SELECT c.cluster_id, ch
FROM cve_notification_configs c, unnest(c.channel_ids) AS ch
WHERE EXISTS (SELECT 1 FROM notification_channels nc WHERE nc.id = ch)
ON CONFLICT DO NOTHING;
