-- 000105_session_previous_token_hash.up.sql
--
-- A refresh rotates the session's refresh token, and the old token's hash used
-- to be overwritten and forgotten. That left a sign-out that races a refresh
-- with nothing to find. Logout looks the session up by the hash of the cookie it
-- was sent; when that cookie is one rotation behind (the request left the
-- browser before the refresh's Set-Cookie landed) no row carries the hash any
-- more, so nothing was revoked and the answer was still "Logged out
-- successfully" — and the refresh's own response then handed the browser a
-- valid cookie for the session that was meant to end.
--
-- previous_token_hash is the hash the session had before its last rotation and
-- rotated_at is when that happened. Logout, and only Logout, may match on the
-- previous hash, and only for a short window after rotated_at
-- (auth.PreviousTokenRevocationWindow); a refresh never does, so a token that
-- has been rotated away still cannot be exchanged for a new one.
--
-- The rotation itself also becomes conditional on the hash it was validated
-- against and on the session still being live (queries/sessions.sql,
-- RotateSessionToken), which is what stops a refresh that loses a race with a
-- revoke from minting an access token for the session it lost to. That needs no
-- schema change; it is mentioned here because the two halves go together.
--
-- Additive and nullable: existing rows keep NULL in both columns and have no
-- previous token until their next refresh, so there is nothing to backfill and
-- an install that upgrades mid-session loses nobody. The index is partial
-- because only a row that has rotated has anything to look up; it serves the
-- Logout lookup and is not on any hot path (one lookup per sign-out).
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS previous_token_hash TEXT;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS rotated_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_sessions_previous_token_hash
    ON sessions (previous_token_hash) WHERE previous_token_hash IS NOT NULL;
