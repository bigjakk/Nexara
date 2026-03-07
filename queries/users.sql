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
SELECT count(*) FROM users WHERE is_active = true;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;
