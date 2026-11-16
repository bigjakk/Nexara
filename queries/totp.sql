-- name: SetTOTPSecret :exec
UPDATE users SET totp_secret = $2 WHERE id = $1;

-- name: ClearTOTPSecret :exec
UPDATE users SET totp_secret = NULL WHERE id = $1;

-- name: GetUserTOTPSecret :one
SELECT id, totp_secret FROM users WHERE id = $1;

-- name: InsertRecoveryCode :exec
INSERT INTO totp_recovery_codes (user_id, code_hash) VALUES ($1, $2);

-- name: ListRecoveryCodes :many
SELECT id, code_hash FROM totp_recovery_codes WHERE user_id = $1;

-- name: DeleteRecoveryCode :exec
DELETE FROM totp_recovery_codes WHERE id = $1;

-- name: DeleteAllRecoveryCodes :exec
DELETE FROM totp_recovery_codes WHERE user_id = $1;

-- name: CountRecoveryCodes :one
SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1;

-- name: RecordTOTPStepUpProof :execrows
-- Records that the person behind session_id has just signed in at the identity
-- provider again (the SSO step-up), for purpose, until ttl_seconds from now. Decided
-- here against the database's clock: only for a session that is the user's, live (not
-- revoked, not expired) and of an active user, and only when auth_time (the ID token's,
-- in Unix seconds) lies within window_seconds before now and skew_seconds after it. 0
-- rows: refused. A newer proof replaces an older one of the same session and purpose.
INSERT INTO totp_step_up_proofs (session_id, user_id, purpose, expires_at)
SELECT s.id, s.user_id, sqlc.arg(purpose)::text, now() + make_interval(secs => sqlc.arg(ttl_seconds)::float8)
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.id = sqlc.arg(session_id)
  AND s.user_id = sqlc.arg(user_id)
  AND NOT s.is_revoked
  AND s.expires_at > now()
  AND u.is_active
  AND to_timestamp(sqlc.arg(auth_time)::float8) >= now() - make_interval(secs => sqlc.arg(window_seconds)::float8)
  AND to_timestamp(sqlc.arg(auth_time)::float8) <= now() + make_interval(secs => sqlc.arg(skew_seconds)::float8)
ON CONFLICT (session_id, purpose) DO UPDATE
  SET user_id = EXCLUDED.user_id, created_at = now(), expires_at = EXCLUDED.expires_at;

-- name: TOTPStepUpAuthTimeIsFresh :one
-- RecordTOTPStepUpProof's test of auth_time alone, to tell a refusal for a sign-in
-- that was not recent from one for a session that ended.
SELECT COALESCE(to_timestamp(sqlc.arg(auth_time)::float8) >= now() - make_interval(secs => sqlc.arg(window_seconds)::float8)
   AND to_timestamp(sqlc.arg(auth_time)::float8) <= now() + make_interval(secs => sqlc.arg(skew_seconds)::float8), false)::boolean AS fresh;

-- name: ConsumeTOTPStepUpProof :execrows
-- Uses up session_id's proof for purpose, if it is the user's and still valid by the
-- database's clock: 1 row, the proof held and is gone; 0, there was none to use.
DELETE FROM totp_step_up_proofs
WHERE session_id = sqlc.arg(session_id)
  AND user_id = sqlc.arg(user_id)
  AND purpose = sqlc.arg(purpose)::text
  AND expires_at > now();
