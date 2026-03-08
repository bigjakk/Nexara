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
