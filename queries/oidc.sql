-- name: CreateOIDCConfig :one
INSERT INTO oidc_configs (
    name, enabled, issuer_url, client_id, client_secret_encrypted,
    redirect_uri, scopes, email_claim, display_name_claim, groups_claim,
    group_role_mapping, default_role_id, auto_provision, allowed_domains
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14
) RETURNING *;

-- name: GetOIDCConfig :one
SELECT * FROM oidc_configs WHERE id = $1;

-- name: GetEnabledOIDCConfig :one
SELECT * FROM oidc_configs WHERE enabled = true LIMIT 1;

-- name: ListOIDCConfigs :many
SELECT * FROM oidc_configs ORDER BY created_at;

-- name: UpdateOIDCConfig :one
UPDATE oidc_configs SET
    name = $2,
    enabled = $3,
    issuer_url = $4,
    client_id = $5,
    client_secret_encrypted = $6,
    redirect_uri = $7,
    scopes = $8,
    email_claim = $9,
    display_name_claim = $10,
    groups_claim = $11,
    group_role_mapping = $12,
    default_role_id = $13,
    auto_provision = $14,
    allowed_domains = $15
WHERE id = $1
RETURNING *;

-- name: DeleteOIDCConfig :exec
DELETE FROM oidc_configs WHERE id = $1;

-- name: CreateOIDCUser :one
INSERT INTO users (email, password_hash, display_name, is_active, role, auth_source)
VALUES ($1, '', $2, true, 'user', 'oidc')
RETURNING *;

-- name: UpdateOIDCUserProfile :one
UPDATE users SET display_name = $2
WHERE id = $1 AND auth_source = 'oidc'
RETURNING *;

-- name: ListOIDCUsers :many
SELECT id, email, display_name, role, is_active, created_at, updated_at, auth_source
FROM users
WHERE auth_source = 'oidc'
ORDER BY email;
