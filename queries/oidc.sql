-- name: CreateOIDCConfig :one
INSERT INTO oidc_configs (
    name, enabled, issuer_url, client_id, client_secret_encrypted,
    redirect_uri, scopes, email_claim, display_name_claim, groups_claim,
    group_role_mapping, default_role_id, auto_provision, allowed_domains,
    allow_private_address
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14,
    $15
) RETURNING *;

-- name: GetOIDCConfig :one
SELECT * FROM oidc_configs WHERE id = $1;

-- name: GetEnabledOIDCConfig :one
SELECT * FROM oidc_configs WHERE enabled = true ORDER BY created_at, id LIMIT 1;

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
    allowed_domains = $15,
    allow_private_address = $16
WHERE id = $1
RETURNING *;

-- name: DeleteOIDCConfig :exec
DELETE FROM oidc_configs WHERE id = $1;

-- name: CreateOIDCUser :one
-- A new SSO account, bound at creation to the identity that signed in: the ID token's
-- issuer and subject (users_oidc_identity_key refuses a second account for one).
INSERT INTO users (email, password_hash, display_name, is_active, role, auth_source, oidc_issuer, oidc_subject)
VALUES (sqlc.arg(email), '', sqlc.arg(display_name), true, 'user', 'oidc', sqlc.arg(oidc_issuer), sqlc.arg(oidc_subject))
RETURNING *;

-- name: GetUserByOIDCIdentity :one
-- The SSO account bound to this provider identity, whatever its address says now.
SELECT * FROM users
WHERE oidc_issuer = sqlc.arg(oidc_issuer) AND oidc_subject = sqlc.arg(oidc_subject)
  AND oidc_subject <> '' AND auth_source = 'oidc';

-- name: LinkOIDCIdentity :one
-- Binds an SSO account that has no identity stored to this one, and only such an
-- account: no row when it is of another source or already bound (to this identity or
-- another), so two first sign-ins that race cannot both write, and a stored link is
-- never overwritten. users_oidc_identity_key refuses an identity another account holds.
UPDATE users SET oidc_issuer = sqlc.arg(oidc_issuer), oidc_subject = sqlc.arg(oidc_subject)
WHERE id = sqlc.arg(id) AND auth_source = 'oidc' AND oidc_subject = '' AND oidc_issuer = ''
  AND sqlc.arg(oidc_issuer)::text <> '' AND sqlc.arg(oidc_subject)::text <> ''
RETURNING *;

-- name: ClearOIDCIdentity :execrows
-- An administrator's reset of one SSO account's link: its next sign-in links again by
-- address. 0 rows: not an SSO account, or not linked.
UPDATE users SET oidc_issuer = '', oidc_subject = ''
WHERE id = sqlc.arg(id) AND auth_source = 'oidc' AND oidc_subject <> '';

-- name: ClearAllOIDCIdentities :many
-- The reset of every SSO account's link at once (after an issuer change), returning
-- the accounts it cleared.
UPDATE users SET oidc_issuer = '', oidc_subject = ''
WHERE auth_source = 'oidc' AND oidc_subject <> ''
RETURNING id;

-- name: CountUsersLinkedToIssuer :one
-- How many accounts are bound to an identity of this issuer: the accounts a change of
-- a configuration's issuer would leave refused at sign-in.
SELECT count(*) FROM users WHERE oidc_issuer = sqlc.arg(oidc_issuer) AND oidc_subject <> '';

-- name: UpdateOIDCUserProfile :one
UPDATE users SET display_name = $2
WHERE id = $1 AND auth_source = 'oidc'
RETURNING *;

-- name: ListOIDCUsers :many
SELECT id, email, display_name, role, is_active, created_at, updated_at, auth_source
FROM users
WHERE auth_source = 'oidc'
ORDER BY email;
