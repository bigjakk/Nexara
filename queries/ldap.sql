-- name: CreateLDAPConfig :one
INSERT INTO ldap_configs (
    name, enabled, server_url, start_tls, skip_tls_verify,
    bind_dn, bind_password_encrypted, search_base_dn,
    user_filter, username_attribute, email_attribute, display_name_attribute,
    group_search_base_dn, group_filter, group_attribute,
    group_role_mapping, default_role_id, sync_interval_minutes
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11, $12,
    $13, $14, $15,
    $16, $17, $18
) RETURNING *;

-- name: GetLDAPConfig :one
SELECT * FROM ldap_configs WHERE id = $1;

-- name: GetEnabledLDAPConfig :one
SELECT * FROM ldap_configs WHERE enabled = true LIMIT 1;

-- name: ListLDAPConfigs :many
SELECT * FROM ldap_configs ORDER BY created_at;

-- name: UpdateLDAPConfig :one
UPDATE ldap_configs SET
    name = $2,
    enabled = $3,
    server_url = $4,
    start_tls = $5,
    skip_tls_verify = $6,
    bind_dn = $7,
    bind_password_encrypted = $8,
    search_base_dn = $9,
    user_filter = $10,
    username_attribute = $11,
    email_attribute = $12,
    display_name_attribute = $13,
    group_search_base_dn = $14,
    group_filter = $15,
    group_attribute = $16,
    group_role_mapping = $17,
    default_role_id = $18,
    sync_interval_minutes = $19
WHERE id = $1
RETURNING *;

-- name: DeleteLDAPConfig :exec
DELETE FROM ldap_configs WHERE id = $1;

-- name: UpdateLDAPConfigLastSync :exec
UPDATE ldap_configs SET last_sync_at = now() WHERE id = $1;

-- name: GetUserByEmailAndSource :one
SELECT * FROM users WHERE email = $1 AND auth_source = $2;

-- name: CreateLDAPUser :one
INSERT INTO users (email, password_hash, display_name, is_active, role, auth_source)
VALUES ($1, '', $2, true, 'user', 'ldap')
RETURNING *;

-- name: UpdateLDAPUserProfile :one
UPDATE users SET display_name = $2
WHERE id = $1 AND auth_source = 'ldap'
RETURNING *;

-- name: SetLDAPUserActive :exec
UPDATE users SET is_active = $2
WHERE id = $1 AND auth_source = 'ldap';

-- name: ListLDAPUsers :many
SELECT id, email, display_name, role, is_active, created_at, updated_at, auth_source
FROM users
WHERE auth_source = 'ldap'
ORDER BY email;
