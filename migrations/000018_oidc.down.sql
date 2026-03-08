-- 000018_oidc.down.sql
-- Revert OIDC/SSO integration

-- Restore original auth_source check (local, ldap only)
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_auth_source_check;
ALTER TABLE users ADD CONSTRAINT users_auth_source_check CHECK (auth_source IN ('local', 'ldap'));

DROP TRIGGER IF EXISTS trg_oidc_configs_updated_at ON oidc_configs;
DROP TABLE IF EXISTS oidc_configs;
