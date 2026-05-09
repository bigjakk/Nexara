-- name: CreateUser :one
INSERT INTO users (email, password_hash, display_name, is_active, totp_secret, role)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at DESC;

-- name: UpdateUser :one
UPDATE users
SET email = $2,
    password_hash = $3,
    display_name = $4,
    is_active = $5,
    totp_secret = $6
WHERE id = $1
RETURNING *;

-- name: UpdatePassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: CountUsers :one
-- Counts every login-capable row in users, including deactivated accounts.
-- Excludes only the well-known system actor seeded by 000013_system_user —
-- that row exists for audit/task attribution and is not a real account, so
-- it must not satisfy the "fresh install" check (otherwise /auth/setup-status
-- always returns needs_setup=false and /register can never bootstrap the
-- first admin). Used by the /auth/register bootstrap gate and
-- /auth/setup-status. Filtering by is_active here would let an admin
-- deactivate every user and inadvertently re-open the anonymous-admin path
-- on the next /auth/register call, so we filter by ID exclusion instead.
SELECT count(*) FROM users WHERE id != '00000000-0000-0000-0000-000000000001';

-- name: UpdateUserDisplayName :one
UPDATE users SET display_name = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;
