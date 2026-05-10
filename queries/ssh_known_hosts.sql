-- name: GetSSHKnownHost :one
SELECT * FROM ssh_known_hosts
WHERE cluster_id = $1 AND host = $2 AND port = $3;

-- name: ListSSHKnownHosts :many
SELECT * FROM ssh_known_hosts
WHERE cluster_id = $1
ORDER BY host, port;

-- name: UpsertSSHKnownHost :one
INSERT INTO ssh_known_hosts (cluster_id, host, port, public_key, fingerprint, pinned_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (cluster_id, host, port) DO UPDATE SET
    public_key  = EXCLUDED.public_key,
    fingerprint = EXCLUDED.fingerprint,
    pinned_by   = EXCLUDED.pinned_by,
    pinned_at   = now()
RETURNING *;

-- name: DeleteSSHKnownHost :exec
DELETE FROM ssh_known_hosts
WHERE cluster_id = $1 AND host = $2 AND port = $3;

-- name: DeleteSSHKnownHostByID :exec
DELETE FROM ssh_known_hosts
WHERE id = $1 AND cluster_id = $2;
