-- Remove notification_sent_at from alert_history
ALTER TABLE alert_history
  DROP COLUMN IF EXISTS notification_sent_at;

-- Remove message_template from alert_rules
ALTER TABLE alert_rules
  DROP COLUMN IF EXISTS message_template;

-- Remove teams/telegram channels before restoring constraint
DELETE FROM notification_channels WHERE channel_type IN ('teams', 'telegram');

ALTER TABLE notification_channels
  DROP CONSTRAINT IF EXISTS notification_channels_channel_type_check;

ALTER TABLE notification_channels
  ADD CONSTRAINT notification_channels_channel_type_check
  CHECK (channel_type IN ('email', 'webhook', 'slack', 'discord', 'pagerduty'));
