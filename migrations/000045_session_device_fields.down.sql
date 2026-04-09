-- 000045_session_device_fields.down.sql

DROP INDEX IF EXISTS idx_sessions_device_type;
ALTER TABLE sessions DROP COLUMN IF EXISTS device_id;
ALTER TABLE sessions DROP COLUMN IF EXISTS device_type;
ALTER TABLE sessions DROP COLUMN IF EXISTS device_name;
