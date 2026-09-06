-- name: CreateCluster :one
INSERT INTO clusters (
    name, api_url, token_id, token_secret_encrypted, tls_fingerprint,
    sync_interval_seconds, is_active,
    credential_source, bootstrap_user_id, bootstrap_token_name,
    bootstrap_created_user, bootstrap_created_acl, bootstrap_created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
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

-- name: CountClustersSharingBootstrapUser :one
-- How many OTHER cluster rows authenticate to Proxmox as the same PVE user.
--
-- Asked before revoking anything at delete time. A privsep=0 token inherits its
-- owner's privileges outright, and a privsep=1 token's effective rights are the
-- intersection with its owner's — so removing the owner, or its only role
-- grant, silently kills every other token on that account. The sibling cluster
-- stays listed in Nexara and simply stops being able to reach Proxmox.
--
-- Matched on the OWNER PARSED OUT OF token_id, not on the bootstrap_* columns:
--
--   * it catches a manually-added cluster that happens to use the same account,
--     which the provenance columns never describe;
--   * it survives Update clearing provenance on a re-pointed cluster;
--   * it does not depend on two rows spelling api_url identically. Host is
--     deliberately NOT compared: the same account name on a genuinely different
--     hypervisor makes this over-report, which costs an un-revoked credential
--     that the audit row names — the opposite mistake costs a working cluster.
--
-- Self-exclusion is explicit rather than relying on the caller having already
-- deleted the row, so this stays correct if it is ever asked before the delete
-- (a dry-run or a preview endpoint).
SELECT count(*) FROM clusters
WHERE id <> sqlc.arg(exclude_cluster_id)
  AND split_part(token_id, '!', 1) = sqlc.arg(bootstrap_user_id);

-- name: ClearClusterCredentialProvenance :exec
-- Forget that Nexara minted this cluster's credential.
--
-- Run when the cluster is re-pointed at a different endpoint or a different
-- token: the recorded PVE user and token name describe objects on the OLD
-- target, and acting on them against the new one would delete something Nexara
-- never created.
UPDATE clusters
SET credential_source      = 'manual',
    bootstrap_user_id      = '',
    bootstrap_token_name   = '',
    bootstrap_created_user = false,
    bootstrap_created_acl  = false,
    bootstrap_created_at   = NULL
WHERE id = $1;

-- name: DeleteCluster :exec
DELETE FROM clusters WHERE id = $1;

-- name: ListActiveClusters :many
SELECT * FROM clusters WHERE is_active = true ORDER BY created_at;

-- name: UpdateClusterPVEVersion :exec
UPDATE clusters SET pve_version = $2 WHERE id = $1;

-- name: UpdateClusterQuorate :exec
UPDATE clusters SET quorate = $2 WHERE id = $1;

-- name: UpdateClusterTLSFingerprint :execrows
-- Narrow on purpose. Re-pinning a rotated certificate is the one field the
-- verify-certificate flow may change, and going through the full UpdateCluster
-- would make it possible to clobber an api_url or credential that someone else
-- edited between the operator seeing the banner and clicking accept.
UPDATE clusters
SET tls_fingerprint = $2,
    updated_at = now()
-- api_url is part of the predicate, not just the row identity: the caller
-- dialled a specific address to obtain this fingerprint, so if someone
-- re-pointed the cluster in between, pinning would attach the old endpoint's
-- certificate to the new address. Zero rows means "it moved, start over".
WHERE id = $1 AND api_url = $3;
