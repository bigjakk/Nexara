-- name: RegisterMobileDevice :one
-- Upserts a device by expo_push_token. The UPDATE branch only fires when the
-- existing row's device_id matches the request — i.e. the same physical
-- install is re-registering. If the device_id differs, the WHERE clause
-- blocks the update and the query returns no rows, so the handler can
-- detect the conflict and return 409 instead of silently reassigning the
-- token to a different account (security review H2: cross-account device
-- hijack).
INSERT INTO mobile_devices (user_id, device_id, device_name, platform, expo_push_token)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (expo_push_token) DO UPDATE SET
    user_id = EXCLUDED.user_id,
    device_name = EXCLUDED.device_name,
    platform = EXCLUDED.platform,
    last_seen_at = now()
WHERE mobile_devices.device_id = EXCLUDED.device_id
  AND mobile_devices.user_id = EXCLUDED.user_id
RETURNING *;

-- name: GetMobileDeviceByExpoToken :one
-- Used for conflict detection when RegisterMobileDevice's WHERE clause
-- blocks an UPSERT.
SELECT * FROM mobile_devices WHERE expo_push_token = $1;

-- name: CountMobileDevicesByUser :one
-- Used to enforce a per-user device cap (security review H3).
SELECT COUNT(*) FROM mobile_devices WHERE user_id = $1;

-- name: ListMobileDevicesByUser :many
SELECT * FROM mobile_devices WHERE user_id = $1 ORDER BY last_seen_at DESC;

-- name: GetMobileDevice :one
SELECT * FROM mobile_devices WHERE id = $1;

-- name: DeleteMobileDevice :exec
DELETE FROM mobile_devices WHERE id = $1;

-- name: DeleteMobileDeviceForUser :exec
DELETE FROM mobile_devices WHERE id = $1 AND user_id = $2;

-- name: DeleteMobileDeviceByExpoToken :exec
DELETE FROM mobile_devices WHERE expo_push_token = $1;

-- name: TouchMobileDevice :exec
UPDATE mobile_devices SET last_seen_at = now() WHERE id = $1;
