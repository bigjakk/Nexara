-- 000017_ldap.up.sql
-- LDAP/AD integration: ldap_configs table + users.auth_source column

CREATE TABLE IF NOT EXISTS ldap_configs (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    TEXT NOT NULL DEFAULT 'Default',
    enabled                 BOOLEAN NOT NULL DEFAULT false,
    server_url              TEXT NOT NULL,
    start_tls               BOOLEAN NOT NULL DEFAULT false,
    skip_tls_verify         BOOLEAN NOT NULL DEFAULT false,
    bind_dn                 TEXT NOT NULL DEFAULT '',
    bind_password_encrypted TEXT NOT NULL DEFAULT '',
    search_base_dn          TEXT NOT NULL,
    user_filter             TEXT NOT NULL DEFAULT '(|(uid={{username}})(mail={{username}}))',
    username_attribute      TEXT NOT NULL DEFAULT 'uid',
    email_attribute         TEXT NOT NULL DEFAULT 'mail',
    display_name_attribute  TEXT NOT NULL DEFAULT 'cn',
    group_search_base_dn    TEXT NOT NULL DEFAULT '',
    group_filter            TEXT NOT NULL DEFAULT '(member={{userDN}})',
    group_attribute         TEXT NOT NULL DEFAULT 'cn',
    group_role_mapping      JSONB NOT NULL DEFAULT '{}',
    default_role_id         UUID REFERENCES roles(id),
    sync_interval_minutes   INTEGER NOT NULL DEFAULT 60,
    last_sync_at            TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_ldap_configs_updated_at
    BEFORE UPDATE ON ldap_configs FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE users ADD COLUMN IF NOT EXISTS auth_source TEXT NOT NULL DEFAULT 'local'
    CHECK (auth_source IN ('local', 'ldap'));
