-- 000105_session_previous_token_hash.down.sql
--
-- Drops the previous-token bookkeeping. Nothing else depends on it: each
-- session keeps its current hash and its revoked flag, so every live session
-- stays signed in. What goes is only the short window in which a sign-out sent
-- with a just-rotated cookie could still find its session.
DROP INDEX IF EXISTS idx_sessions_previous_token_hash;
ALTER TABLE sessions DROP COLUMN IF EXISTS rotated_at;
ALTER TABLE sessions DROP COLUMN IF EXISTS previous_token_hash;
