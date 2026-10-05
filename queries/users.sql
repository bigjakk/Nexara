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

-- name: BumpUserAuthEpoch :execrows
-- Moves the user's auth_epoch to its next value and says how many rows it changed
-- (0: there is no such user). It is the FIRST statement of every revoke-all —
-- before the listing of the live sessions and the revoke of them — and a statement
-- of its own, never part of another: the order and the separation are what let a
-- session created by a sign-in whose credential check is older than this revoke-all
-- be refused or revoked, and never survive. The reasoning, for both orders in which
-- an insert and a revoke-all can meet, is on CreateSessionAtEpoch in sessions.sql.
-- auth.RevokeAllUserSessionsIn is the only caller, and every revoke-all goes
-- through it: ChangePassword (inside its transaction), sign-out of all devices, and
-- the deactivation of an account.
--
-- The UPDATE takes the users row's FOR NO KEY UPDATE lock, which waits for any
-- sign-in's FOR SHARE and is waited for by the next one; inside a caller's
-- transaction the lock is held until that transaction ends, so the sign-ins that
-- raced it decide after the whole revoke-all, not between its statements.
UPDATE users SET auth_epoch = auth_epoch + 1 WHERE id = sqlc.arg(id);

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
