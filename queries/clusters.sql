-- name: CreateCluster :one
INSERT INTO clusters (name, api_url, token_id, token_secret_encrypted, tls_fingerprint, sync_interval_seconds, is_active)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetCluster :one
SELECT * FROM clusters WHERE id = $1;

-- name: ListClusters :many
SELECT * FROM clusters ORDER BY created_at DESC;

-- name: UpdateCluster :one
UPDATE clusters
SET name = $2,
    api_url = $3,
    token_id = $4,
    token_secret_encrypted = $5,
    tls_fingerprint = $6,
    sync_interval_seconds = $7,
    is_active = $8
WHERE id = $1
RETURNING *;

-- name: DeleteCluster :exec
DELETE FROM clusters WHERE id = $1;
