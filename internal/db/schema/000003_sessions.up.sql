-- 000003_sessions.up.sql
-- Add role column to users and create sessions table

-- Add role to users (admin, user)
ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user'
    CHECK (role IN ('admin', 'user'));

-- sessions table for refresh token tracking
CREATE TABLE IF NOT EXISTS sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash      TEXT NOT NULL,
    user_agent      TEXT NOT NULL DEFAULT '',
    ip_address      TEXT NOT NULL DEFAULT '',
    is_revoked      BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    last_used_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions (user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_token_hash ON sessions (token_hash);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions (expires_at);
