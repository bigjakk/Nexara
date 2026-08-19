-- 000083_drop_mobile_devices.down.sql
-- Recreates the mobile_devices table exactly as 000046 defined it.
--
-- Structure only — the rows are gone for good, since the up migration drops
-- them. Rolling back gives you an empty table with the original shape, which
-- is enough for the migration chain to stay consistent; it does NOT bring the
-- React Native app, its /me/devices endpoints or the expo_push dispatcher
-- back, as those were removed in application code, not in this migration.

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
