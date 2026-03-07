-- 000001_initial_schema.up.sql
-- Core tables: users, clusters, pbs_servers, settings

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- users
CREATE TABLE IF NOT EXISTS users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name  TEXT NOT NULL DEFAULT '',
    is_active     BOOLEAN NOT NULL DEFAULT true,
    totp_secret   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users (email);

-- clusters (Proxmox VE)
CREATE TABLE IF NOT EXISTS clusters (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                     TEXT NOT NULL,
    api_url                  TEXT NOT NULL,
    token_id                 TEXT NOT NULL,
    token_secret_encrypted   TEXT NOT NULL,
    tls_fingerprint          TEXT NOT NULL DEFAULT '',
    sync_interval_seconds    INTEGER NOT NULL DEFAULT 30,
    is_active                BOOLEAN NOT NULL DEFAULT true,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- pbs_servers (Proxmox Backup Server)
CREATE TABLE IF NOT EXISTS pbs_servers (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                     TEXT NOT NULL,
    api_url                  TEXT NOT NULL,
    token_id                 TEXT NOT NULL,
    token_secret_encrypted   TEXT NOT NULL,
    cluster_id               UUID REFERENCES clusters(id) ON DELETE SET NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_pbs_servers_cluster_id ON pbs_servers (cluster_id);

-- settings (key-value with scoping)
CREATE TABLE IF NOT EXISTS settings (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key        TEXT NOT NULL,
    value      JSONB NOT NULL DEFAULT '{}',
    scope      TEXT NOT NULL DEFAULT 'global' CHECK (scope IN ('global', 'user', 'cluster')),
    scope_id   UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (key, scope, scope_id)
);

CREATE INDEX IF NOT EXISTS idx_settings_key ON settings (key);
CREATE INDEX IF NOT EXISTS idx_settings_scope ON settings (scope, scope_id);

-- updated_at trigger function
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_clusters_updated_at
    BEFORE UPDATE ON clusters
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_pbs_servers_updated_at
    BEFORE UPDATE ON pbs_servers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_settings_updated_at
    BEFORE UPDATE ON settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
