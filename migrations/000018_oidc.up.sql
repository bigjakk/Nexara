-- 000018_oidc.up.sql
-- OIDC/SSO integration: oidc_configs table + expand users.auth_source

CREATE TABLE IF NOT EXISTS oidc_configs (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                        TEXT NOT NULL DEFAULT 'Default',
    enabled                     BOOLEAN NOT NULL DEFAULT false,
    issuer_url                  TEXT NOT NULL,
    client_id                   TEXT NOT NULL,
    client_secret_encrypted     TEXT NOT NULL DEFAULT '',
    redirect_uri                TEXT NOT NULL DEFAULT '',
    scopes                      TEXT[] NOT NULL DEFAULT ARRAY['openid', 'email', 'profile'],
    email_claim                 TEXT NOT NULL DEFAULT 'email',
    display_name_claim          TEXT NOT NULL DEFAULT 'name',
    groups_claim                TEXT NOT NULL DEFAULT 'groups',
    group_role_mapping          JSONB NOT NULL DEFAULT '{}',
    default_role_id             UUID REFERENCES roles(id),
    auto_provision              BOOLEAN NOT NULL DEFAULT true,
    allowed_domains             TEXT[] NOT NULL DEFAULT '{}',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_oidc_configs_updated_at
    BEFORE UPDATE ON oidc_configs FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Expand auth_source check to include 'oidc'
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_auth_source_check;
ALTER TABLE users ADD CONSTRAINT users_auth_source_check CHECK (auth_source IN ('local', 'ldap', 'oidc'));
