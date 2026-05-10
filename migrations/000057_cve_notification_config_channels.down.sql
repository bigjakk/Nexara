-- 000057_cve_notification_config_channels.down.sql
--
-- Drops the join table introduced by 4.8a. Round-trips losslessly because
-- the up migration was dual-write only (array remains the source of truth
-- in this release), so dropping the join table loses nothing the array
-- doesn't already encode.

DROP INDEX IF EXISTS idx_cve_notification_config_channels_channel_id;
DROP TABLE IF EXISTS cve_notification_config_channels;
