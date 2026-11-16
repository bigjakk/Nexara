ALTER TABLE ldap_configs DROP COLUMN IF EXISTS sync_lease_expires_at;
ALTER TABLE ldap_configs DROP COLUMN IF EXISTS sync_lease_token;
ALTER TABLE users DROP COLUMN IF EXISTS ldap_login_name;
ALTER TABLE users DROP COLUMN IF EXISTS deactivated_by_directory;
