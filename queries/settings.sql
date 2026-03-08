-- name: GetSetting :one
SELECT id, key, value, scope, scope_id, created_at, updated_at
FROM settings
WHERE key = $1 AND scope = $2 AND (scope_id = $3 OR (scope_id IS NULL AND $3::uuid IS NULL))
LIMIT 1;

-- name: ListSettingsByScope :many
SELECT id, key, value, scope, scope_id, created_at, updated_at
FROM settings
WHERE scope = $1 AND (scope_id = $2 OR (scope_id IS NULL AND $2::uuid IS NULL))
ORDER BY key;

-- name: UpsertSetting :one
INSERT INTO settings (key, value, scope, scope_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (key, scope, scope_id)
DO UPDATE SET value = EXCLUDED.value, updated_at = now()
RETURNING id, key, value, scope, scope_id, created_at, updated_at;

-- name: DeleteSetting :exec
DELETE FROM settings
WHERE key = $1 AND scope = $2 AND (scope_id = $3 OR (scope_id IS NULL AND $3::uuid IS NULL));

-- name: DeleteSettingByID :exec
DELETE FROM settings WHERE id = $1;

-- name: ListGlobalSettings :many
SELECT id, key, value, scope, scope_id, created_at, updated_at
FROM settings
WHERE scope = 'global' AND scope_id IS NULL
ORDER BY key;
