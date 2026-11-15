ALTER TABLE ldap_configs DROP COLUMN IF EXISTS sync_lease_expires_at;
ALTER TABLE ldap_configs DROP COLUMN IF EXISTS sync_lease_token;
ALTER TABLE users DROP COLUMN IF EXISTS deactivated_by_directory;
