-- 000046_mobile_devices.up.sql
-- Tracks mobile devices registered for push notifications via the Expo Push API.

CREATE TABLE IF NOT EXISTS mobile_devices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id       TEXT NOT NULL,
    device_name     TEXT NOT NULL,
    platform        TEXT NOT NULL CHECK (platform IN ('ios', 'android')),
    expo_push_token TEXT NOT NULL UNIQUE,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mobile_devices_user ON mobile_devices(user_id);
CREATE INDEX IF NOT EXISTS idx_mobile_devices_device ON mobile_devices(user_id, device_id);
