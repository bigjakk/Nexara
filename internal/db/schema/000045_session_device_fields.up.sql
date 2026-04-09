-- 000045_session_device_fields.up.sql
-- Add optional device tracking columns to sessions for mobile app support.
-- All columns are nullable so existing rows and existing callers continue to work unchanged.

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS device_name TEXT;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS device_type TEXT
    CHECK (device_type IS NULL OR device_type IN ('web', 'mobile', 'desktop'));
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS device_id TEXT;

CREATE INDEX IF NOT EXISTS idx_sessions_device_type ON sessions (device_type) WHERE device_type IS NOT NULL;
