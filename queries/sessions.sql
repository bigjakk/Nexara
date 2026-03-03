-- name: CreateSession :one
INSERT INTO sessions (user_id, token_hash, user_agent, ip_address, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetSessionByID :one
SELECT * FROM sessions WHERE id = $1;

-- name: GetSessionByTokenHash :one
SELECT * FROM sessions WHERE token_hash = $1 AND is_revoked = false;

-- name: RevokeSession :exec
UPDATE sessions SET is_revoked = true WHERE id = $1;

-- name: RevokeAllUserSessions :exec
UPDATE sessions SET is_revoked = true WHERE user_id = $1;

-- name: UpdateSessionTokenHash :exec
UPDATE sessions SET token_hash = $2, last_used_at = now() WHERE id = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < now();

-- name: ListUserSessions :many
SELECT * FROM sessions WHERE user_id = $1 AND is_revoked = false AND expires_at > now()
ORDER BY created_at DESC;
