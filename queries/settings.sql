-- name: GetSetting :one
-- ORDER BY makes the LIMIT 1 deterministic. 000075 deduped the shared-scope
-- rows that a plain UNIQUE let accumulate and now prevents new ones, so a
-- duplicate should be unreachable — but an unordered LIMIT 1 would silently
-- resolve to the planner's choice (in practice the oldest, i.e. most stale,
-- heap tuple) if one ever appeared again. Newest write wins instead.
--
-- id is the final tiebreak so the ordering is total: rows written in the same
-- transaction share now(), and updated_at alone would degenerate back to heap
-- order. Matches the dedup ordering in 000075.
SELECT id, key, value, scope, scope_id, created_at, updated_at
FROM settings
WHERE key = $1 AND scope = $2 AND (scope_id = $3 OR (scope_id IS NULL AND $3::uuid IS NULL))
ORDER BY updated_at DESC, created_at DESC, id DESC
LIMIT 1;

-- name: ListSettingsByScope :many
-- Deliberately no row-level tiebreak, unlike GetSetting above. 000075 makes a
-- duplicate (key, scope, scope_id) unreachable; if one somehow appeared, a list
-- surfaces it as a repeated key rather than silently resolving it, so there is
-- nothing to disambiguate. A tiebreak would also have to match each caller's
-- fold direction — GetBranding builds a last-wins map, so it would need ASC
-- where GetSetting needs DESC — which is a sharper edge than leaving it off.
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
-- No row-level tiebreak, for the reasons noted on ListSettingsByScope above.
SELECT id, key, value, scope, scope_id, created_at, updated_at
FROM settings
WHERE scope = 'global' AND scope_id IS NULL
ORDER BY key;
