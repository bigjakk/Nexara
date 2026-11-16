DROP INDEX IF EXISTS users_ldap_entry_key;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_ldap_entry_check;
ALTER TABLE users DROP COLUMN IF EXISTS ldap_entry_id;
ALTER TABLE users DROP COLUMN IF EXISTS ldap_config_id;
