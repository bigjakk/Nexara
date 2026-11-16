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
SELECT * FROM ldap_configs WHERE enabled = true ORDER BY created_at, id LIMIT 1;

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
-- GetUserByEmail's match (exact spelling first, else the one account differing only by
-- case, between plain-ASCII addresses), of the given source.
SELECT * FROM users
WHERE (users.email = sqlc.arg(email)::text
       OR (octet_length(sqlc.arg(email)::text) = char_length(sqlc.arg(email)::text)
           AND octet_length(users.email) = char_length(users.email)
           AND lower(users.email) = lower(sqlc.arg(email)::text)
           AND (SELECT count(*) FROM users y WHERE octet_length(y.email) = char_length(y.email)
                  AND lower(y.email) = lower(sqlc.arg(email)::text)) = 1))
  AND users.auth_source = sqlc.arg(auth_source);

-- name: CreateLDAPUser :one
-- A new directory account, bound at creation to the entry that signed in when the
-- directory exposes an identifier (config_id NULL and entry_id '' when it does not);
-- users_ldap_entry_key refuses a second account for one entry under one configuration.
INSERT INTO users (email, password_hash, display_name, is_active, role, auth_source, ldap_config_id, ldap_entry_id)
VALUES (sqlc.arg(email), '', sqlc.arg(display_name), true, 'user', 'ldap', sqlc.narg(config_id)::uuid, sqlc.arg(entry_id))
RETURNING *;

-- name: GetUserByLDAPEntry :one
-- The directory account bound to this entry under this configuration, whatever its
-- address says now.
SELECT * FROM users
WHERE ldap_config_id = sqlc.arg(config_id)::uuid AND ldap_entry_id = sqlc.arg(entry_id)
  AND ldap_entry_id <> '' AND auth_source = 'ldap';

-- name: LinkLDAPEntry :one
-- Binds a directory account to this entry under this configuration, and only one not
-- bound under it already: no row when it is of another source or bound under this
-- configuration (to this entry or another), so two first sign-ins or a sign-in and a
-- sync that race cannot both write, and a stored binding is never overwritten. An
-- account bound under another configuration, or one since deleted, is bound again.
-- users_ldap_entry_key refuses an entry another account holds.
UPDATE users SET ldap_config_id = sqlc.arg(config_id)::uuid, ldap_entry_id = sqlc.arg(entry_id)
WHERE id = sqlc.arg(id) AND auth_source = 'ldap'
  AND ldap_config_id IS DISTINCT FROM sqlc.arg(config_id)::uuid
  AND sqlc.arg(entry_id)::text <> ''
RETURNING *;

-- name: ClearLDAPEntry :execrows
-- An administrator's reset of one directory account's binding: its next sign-in or
-- sync binds it again by address. 0 rows: not a directory account, or not bound.
UPDATE users SET ldap_config_id = NULL, ldap_entry_id = ''
WHERE id = sqlc.arg(id) AND auth_source = 'ldap' AND ldap_config_id IS NOT NULL;

-- name: FollowLDAPEntryAddress :one
-- Moves a bound directory account to the address its entry now has, unless another
-- account holds that address in any case (the rule of EmailTakenIgnoringCase): no row
-- then, and the account keeps the address it has. Only for an account bound under
-- this configuration, which is what makes the entry its own.
UPDATE users SET email = sqlc.arg(email)::text
WHERE users.id = sqlc.arg(id) AND users.auth_source = 'ldap' AND users.ldap_config_id = sqlc.arg(config_id)::uuid
  AND NOT EXISTS (SELECT 1 FROM users y WHERE y.id <> sqlc.arg(id)
      AND (y.email = sqlc.arg(email)::text
           OR (octet_length(sqlc.arg(email)::text) = char_length(sqlc.arg(email)::text)
               AND octet_length(y.email) = char_length(y.email)
               AND lower(y.email) = lower(sqlc.arg(email)::text))))
RETURNING *;

-- name: UpdateLDAPUserProfile :one
UPDATE users SET display_name = $2
WHERE id = $1 AND auth_source = 'ldap'
RETURNING *;

-- name: DeactivateLDAPUserForDirectory :execrows
-- A sync did not find the account in the directory. Only an active account: one an
-- administrator deactivated in the meantime keeps their reason, deactivated by hand.
UPDATE users SET is_active = false, deactivated_by_directory = true
WHERE id = $1 AND auth_source = 'ldap' AND is_active;

-- name: ReactivateLDAPUserFromDirectory :execrows
-- A sync found again an account that a sync had deactivated. One deactivated by hand,
-- or reactivated by an administrator in the meantime, is not touched.
UPDATE users SET is_active = true, deactivated_by_directory = false
WHERE id = $1 AND auth_source = 'ldap' AND NOT is_active AND deactivated_by_directory;

-- name: SetLDAPLoginName :exec
-- Keeps the name a directory sign-in found the account's entry with, for the sync's
-- search. Never logged or audited: it is what the person typed.
UPDATE users SET ldap_login_name = sqlc.arg(login_name)
WHERE id = sqlc.arg(id) AND auth_source = 'ldap' AND ldap_login_name <> sqlc.arg(login_name);

-- name: ListLDAPUsers :many
SELECT id, email, display_name, role, is_active, created_at, updated_at, auth_source, deactivated_by_directory, ldap_login_name,
       ldap_config_id, ldap_entry_id
FROM users
WHERE auth_source = 'ldap'
ORDER BY email;

-- name: ClaimLDAPSyncLease :one
-- Claims the config's sync lease for lease_seconds, when no sync holds one that is
-- still running, and returns the config; no row when another sync holds it (or the
-- config is gone). One sync per config at a time, whichever replica runs it.
UPDATE ldap_configs
SET sync_lease_token = sqlc.arg(token)::uuid,
    sync_lease_expires_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::double precision)
WHERE id = sqlc.arg(id)
  AND (sync_lease_expires_at IS NULL OR sync_lease_expires_at <= now())
RETURNING *;

-- name: RenewLDAPSyncLease :execrows
-- Moves the lease's end on, for the sync that holds it; 0 rows: it no longer does.
UPDATE ldap_configs
SET sync_lease_expires_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::double precision)
WHERE id = sqlc.arg(id) AND sync_lease_token = sqlc.arg(token)::uuid;

-- name: ReleaseLDAPSyncLease :exec
-- Releases the lease this sync claimed, and only that one.
UPDATE ldap_configs SET sync_lease_token = NULL, sync_lease_expires_at = NULL
WHERE id = sqlc.arg(id) AND sync_lease_token = sqlc.arg(token)::uuid;

-- name: ListDirectoryConfigsUsingRole :many
-- The LDAP and OIDC configs whose default role is this one. Their default_role_id
-- references roles(id) with no ON DELETE, so the role cannot be deleted while any
-- names it; DeleteRole answers 409 with this list instead of the foreign-key error.
SELECT 'ldap'::text AS kind, name FROM ldap_configs WHERE default_role_id = sqlc.arg(role_id)::uuid
UNION ALL
SELECT 'oidc'::text AS kind, name FROM oidc_configs WHERE default_role_id = sqlc.arg(role_id)::uuid
ORDER BY kind, name;
