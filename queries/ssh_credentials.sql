-- name: GetClusterSSHCredentials :one
SELECT * FROM cluster_ssh_credentials WHERE cluster_id = $1;

-- name: UpsertClusterSSHCredentials :one
INSERT INTO cluster_ssh_credentials (cluster_id, username, port, auth_type, encrypted_password, encrypted_private_key)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (cluster_id) DO UPDATE SET
    username = EXCLUDED.username,
    port = EXCLUDED.port,
    auth_type = EXCLUDED.auth_type,
    encrypted_password = EXCLUDED.encrypted_password,
    encrypted_private_key = EXCLUDED.encrypted_private_key,
    updated_at = now()
RETURNING *;

-- name: DeleteClusterSSHCredentials :exec
DELETE FROM cluster_ssh_credentials WHERE cluster_id = $1;

-- name: HasClusterSSHCredentials :one
SELECT EXISTS(SELECT 1 FROM cluster_ssh_credentials WHERE cluster_id = $1) AS has_credentials;
