-- name: CreatePBSServer :one
INSERT INTO pbs_servers (name, api_url, token_id, token_secret_encrypted, cluster_id, tls_fingerprint)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPBSServer :one
SELECT * FROM pbs_servers WHERE id = $1;

-- name: ListPBSServers :many
SELECT * FROM pbs_servers ORDER BY created_at DESC;

-- name: ListPBSServersByCluster :many
SELECT * FROM pbs_servers WHERE cluster_id = $1 ORDER BY created_at DESC;

-- name: UpdatePBSServer :one
UPDATE pbs_servers
SET name = $2,
    api_url = $3,
    token_id = $4,
    token_secret_encrypted = $5,
    cluster_id = $6,
    tls_fingerprint = $7
WHERE id = $1
RETURNING *;

-- name: DeletePBSServer :exec
DELETE FROM pbs_servers WHERE id = $1;

-- name: ListActivePBSServers :many
SELECT * FROM pbs_servers ORDER BY created_at ASC;
