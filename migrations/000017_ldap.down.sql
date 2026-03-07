-- 000017_ldap.down.sql

ALTER TABLE users DROP COLUMN IF EXISTS auth_source;
DROP TRIGGER IF EXISTS trg_ldap_configs_updated_at ON ldap_configs;
DROP TABLE IF EXISTS ldap_configs;
