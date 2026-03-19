-- name: CreateAPIKey :one
INSERT INTO api_keys (user_id, name, key_prefix, key_hash, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAPIKeyByHash :one
SELECT ak.id, ak.user_id, ak.name, ak.key_prefix, ak.key_hash,
       ak.expires_at, ak.last_used_at, ak.last_used_ip, ak.is_revoked, ak.created_at,
       u.email AS user_email, u.role AS user_role, u.is_active AS user_is_active
FROM api_keys ak
JOIN users u ON u.id = ak.user_id
WHERE ak.key_hash = $1
  AND ak.is_revoked = false
  AND (ak.expires_at IS NULL OR ak.expires_at > now());

-- name: ListAPIKeysByUser :many
SELECT id, user_id, name, key_prefix, expires_at, last_used_at, last_used_ip, is_revoked, created_at
FROM api_keys
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: ListAllAPIKeys :many
SELECT ak.id, ak.user_id, ak.name, ak.key_prefix, ak.expires_at,
       ak.last_used_at, ak.last_used_ip, ak.is_revoked, ak.created_at,
       u.email AS user_email, u.display_name AS user_display_name
FROM api_keys ak
JOIN users u ON u.id = ak.user_id
ORDER BY ak.created_at DESC;

-- name: RevokeAPIKey :exec
UPDATE api_keys SET is_revoked = true WHERE id = $1;

-- name: RevokeAllUserAPIKeys :exec
UPDATE api_keys SET is_revoked = true WHERE user_id = $1 AND is_revoked = false;

-- name: UpdateAPIKeyLastUsed :exec
UPDATE api_keys SET last_used_at = now(), last_used_ip = $2 WHERE id = $1;

-- name: CountActiveAPIKeysByUser :one
SELECT count(*) FROM api_keys WHERE user_id = $1 AND is_revoked = false;

-- name: GetAPIKeyByID :one
SELECT * FROM api_keys WHERE id = $1;
