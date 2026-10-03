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

-- name: UpdatePassword :execrows
-- A compare-and-swap on the password hash the caller verified, not a plain
-- overwrite: it sets the new hash only on a row whose current hash is
-- expected_hash, and says how many rows it changed. Two requests that proved the
-- same old password cannot both succeed (the later one matches nothing once the
-- earlier has committed: under READ COMMITTED it waits for the row lock, then
-- re-evaluates the WHERE against the new row), and an account deleted since the
-- check matches nothing either. The caller treats 0 rows as "nothing was
-- changed" and rolls back.
UPDATE users
SET password_hash = sqlc.arg(password_hash)
WHERE id = sqlc.arg(id)
  AND password_hash = sqlc.arg(expected_hash);

-- name: GetPasswordHashForSettle :one
-- Reads the stored password hash the way a caller must when it wants to know
-- whether a transaction that may still be running has changed it: a LOCKING
-- read. A plain SELECT sees the newest COMMITTED version, and a transaction whose
-- COMMIT the server has received but not finished — the commit record not yet
-- flushed, or the wait for a synchronous replica not over — has not committed
-- yet, so a plain read in that window finds the old hash and calls a change that
-- is about to land one that did not. FOR SHARE conflicts with the row lock an
-- UPDATE takes, so it waits for that transaction to end and, under READ
-- COMMITTED, then returns the newest committed version: the new hash if the
-- transaction committed, the old one if it rolled back. The caller bounds the
-- wait with its context; a wait that outlasts it is an answer of "could not
-- tell". Used by ChangePassword, and only to settle a COMMIT whose answer was
-- lost.
SELECT password_hash FROM users WHERE id = sqlc.arg(id) FOR SHARE;

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
