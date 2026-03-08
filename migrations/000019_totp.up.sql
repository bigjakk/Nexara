-- 000019_totp.up.sql
-- TOTP two-factor authentication: recovery codes table

CREATE TABLE IF NOT EXISTS totp_recovery_codes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_totp_recovery_codes_user_id ON totp_recovery_codes (user_id);
