-- name: CreateVeeamServer :one
INSERT INTO veeam_servers (
    name, base_url, username, password_encrypted,
    api_revision, product_version, license_edition,
    tls_fingerprint, verify_tls, enabled
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetVeeamServer :one
SELECT * FROM veeam_servers WHERE id = $1;

-- name: ListVeeamServers :many
SELECT * FROM veeam_servers ORDER BY created_at DESC;

-- name: UpdateVeeamServer :one
UPDATE veeam_servers
SET name = $2,
    base_url = $3,
    username = $4,
    password_encrypted = $5,
    api_revision = $6,
    product_version = $7,
    license_edition = $8,
    tls_fingerprint = $9,
    verify_tls = $10,
    enabled = $11
WHERE id = $1
RETURNING *;

-- name: DeleteVeeamServer :exec
DELETE FROM veeam_servers WHERE id = $1;
