-- Expand channel_type CHECK to include 'teams' and 'telegram'
ALTER TABLE notification_channels
  DROP CONSTRAINT IF EXISTS notification_channels_channel_type_check;

ALTER TABLE notification_channels
  ADD CONSTRAINT notification_channels_channel_type_check
  CHECK (channel_type IN ('email', 'webhook', 'slack', 'discord', 'pagerduty', 'teams', 'telegram'));

-- Add optional message_template column to alert_rules for custom notification templates
ALTER TABLE alert_rules
  ADD COLUMN IF NOT EXISTS message_template TEXT NOT NULL DEFAULT '';

-- Track when notifications were sent for idempotency
ALTER TABLE alert_history
  ADD COLUMN IF NOT EXISTS notification_sent_at TIMESTAMPTZ;
