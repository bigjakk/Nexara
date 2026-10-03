-- name: CreateSession :one
INSERT INTO sessions (user_id, token_hash, user_agent, ip_address, expires_at, device_name, device_type, device_id, user_role)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetSessionByID :one
SELECT * FROM sessions WHERE id = $1;

-- name: GetSessionByTokenHash :one
-- The token-issuing lookup: refresh, and the session list's is_current. It
-- matches the session's CURRENT hash only. A token that has been rotated away is
-- in previous_token_hash and must never authenticate a refresh, so nothing here
-- may be loosened to read that column.
SELECT * FROM sessions WHERE token_hash = $1 AND is_revoked = false;

-- name: GetSessionByPreviousTokenHash :one
-- NEVER AUTHENTICATES. Finds the live session whose refresh token was rotated
-- away from the given hash within the last window_seconds. It serves two callers
-- and both only DECIDE something: Logout, so that a sign-out sent with a cookie
-- one rotation behind (it left the browser before a refresh's Set-Cookie landed)
-- still reaches the session it was meant for; and Refresh, to tell a refusal that
-- lost a race to a concurrent refresh from any other. Neither may use it to
-- decide which session is "current" or to issue anything: GetSessionByTokenHash
-- is the only lookup that may do either.
--
-- "Live" and the window are part of the predicate, here, so that no caller can
-- forget either: a revoked session or an expired one matches nothing, so a caller
-- gets a live session or no row, and a rotated-away token cannot be matched for
-- longer than the window the caller names. A NULL rotated_at compares as unknown
-- and never matches; the same goes for a window of zero or less, so both fail
-- closed.
--
-- The window is measured from rotated_at, which RotateSessionToken stamps with the
-- database clock as the UPDATE runs, to now() here, which for this single
-- autocommit statement is the moment it started. Both are the database's clock,
-- so the application's cannot skew the comparison.
SELECT * FROM sessions
WHERE previous_token_hash = sqlc.arg(token_hash)::text
  AND is_revoked = false
  AND expires_at > now()
  AND rotated_at > now() - make_interval(secs => sqlc.arg(window_seconds)::float);

-- name: RevokeSession :exec
UPDATE sessions SET is_revoked = true WHERE id = $1;

-- name: RevokeAllUserSessions :exec
UPDATE sessions SET is_revoked = true WHERE user_id = $1;

-- name: RotateSessionToken :execrows
-- Rotates a session's refresh token, and reports how many rows it changed. It is
-- the only statement that rotates one, and 0 rows means REFUSED: the session is
-- no longer what the refresh validated, so no token may be issued for it.
-- auth.RotateRefreshToken turns that into an error and is its only caller.
--
-- It is conditional because the validation before it (GetSessionByTokenHash) is
-- a separate statement. Unconditional, a refresh that validated a moment before
-- a sign-out revoked the session still wrote a new hash and went on to mint a
-- 15-minute access token and a refresh cookie for a session that had just been
-- ended; and two refreshes presenting the same cookie both succeeded, each
-- handing back a different new one, of which the browser kept whichever landed
-- last. The conditions:
--   id             the session being rotated
--   old_token_hash the hash this refresh presented. Only the session's CURRENT
--                  hash rotates; one already rotated away by a concurrent
--                  refresh matches nothing, so exactly one of two racing
--                  refreshes wins.
--   is_revoked     a revoke (Logout, LogoutAll, DELETE /auth/sessions/:id, a
--                  password change, an admin deactivation) beats a refresh that
--                  has not yet rotated.
--   expires_at     the session may have expired since it was validated.
--
-- What each way of meeting a revoke or a second refresh comes to. Postgres runs
-- this at READ COMMITTED, the default, which nothing here changes:
--   * The other change is already committed when this statement reads the row, or
--     commits before the row is locked, including while this statement waits on
--     it: the WHERE re-check against the newest row version catches it. 0 rows,
--     refused. This is also what makes exactly one of two refreshes win: the
--     second waits for the first and finds its old hash gone.
--   * This statement locks the row first: a revoke arriving meanwhile waits for
--     this transaction to commit and then sets is_revoked on top of the rotated
--     row. The refresh won, and the session still ends revoked.
-- internal/db/session_rotation_db_test.go drives these against Postgres.
--
-- previous_token_hash = token_hash reads the OLD row: every expression in an
-- UPDATE's SET list sees the row as it was before the statement, which is why
-- the two columns swap rather than both ending up as the new hash.
--
-- rotated_at is what bounds GetSessionByPreviousTokenHash's window, so it is
-- stamped with clock_timestamp(), the database clock as this UPDATE runs, and not
-- with now(), which is the start of the TRANSACTION. Refresh runs this a few
-- statements after it begins, and a transaction that had waited for a connection
-- or a lock would otherwise date the rotation by that wait and eat into windows
-- that are only seconds long. It is still a few milliseconds before the commit,
-- which is as close as a stamp written inside the transaction can be.
--
-- user_role is written on every rotation, and that is load-bearing: it is how a
-- session issued before 000055 recorded the role (until then its role is empty),
-- and Refresh's role-rotation guard accepts an empty role only until this fills
-- it in.
UPDATE sessions
SET previous_token_hash = token_hash,
    rotated_at          = clock_timestamp(),
    token_hash          = sqlc.arg(new_token_hash),
    user_role           = sqlc.arg(user_role),
    last_used_at        = now()
WHERE id = sqlc.arg(id)
  AND token_hash = sqlc.arg(old_token_hash)
  AND is_revoked = false
  AND expires_at > now();

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < now();

-- name: ListUserSessions :many
SELECT * FROM sessions WHERE user_id = $1 AND is_revoked = false AND expires_at > now()
ORDER BY created_at DESC;
