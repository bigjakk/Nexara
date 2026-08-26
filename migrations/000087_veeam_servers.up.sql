-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000087_veeam_servers.up.sql
-- Phase 1 of the Veeam Backup & Replication integration: the connection
-- foundation. Adds the server registry, the Veeam-platform → Nexara-cluster
-- mapping, and the view/manage/delete:veeam permission family.
--
-- Purely additive — two new tables and three new permission rows. Nothing is
-- renamed, retyped or dropped, so an existing install upgrades unattended.
--
-- Veeam is a SECOND backup provider alongside PBS, modelled on pbs_servers but
-- deliberately not folded into it: VBR authenticates with an OAuth2 password
-- grant against a single admin account rather than a scoped API token, and one
-- Veeam server can protect N Proxmox clusters where a PBS server maps to at
-- most one. Those two differences are what the schema below encodes.

CREATE TABLE IF NOT EXISTS veeam_servers (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 TEXT NOT NULL,
    base_url             TEXT NOT NULL,
    username             TEXT NOT NULL,
    password_encrypted   TEXT NOT NULL,
    api_revision         TEXT NOT NULL DEFAULT '',
    product_version      TEXT NOT NULL DEFAULT '',
    license_edition      TEXT NOT NULL DEFAULT '',
    tls_fingerprint      TEXT NOT NULL DEFAULT '',
    verify_tls           BOOLEAN NOT NULL DEFAULT true,
    enabled              BOOLEAN NOT NULL DEFAULT true,
    last_sync_at         TIMESTAMPTZ,
    last_sync_error      TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE veeam_servers IS 'Registered Veeam Backup & Replication servers (VBR 13.1+, Enterprise Plus)';
COMMENT ON COLUMN veeam_servers.base_url IS 'https://host:9419 — the REST API root, NOT the console URL';
COMMENT ON COLUMN veeam_servers.username IS 'Admin account, often DOMAIN\user. The backslash is significant and must survive to the OAuth2 form body verbatim';
COMMENT ON COLUMN veeam_servers.password_encrypted IS 'AES-256-GCM ciphertext under ENCRYPTION_KEY. Never returned by the API, never logged, never written to an audit row';
COMMENT ON COLUMN veeam_servers.api_revision IS 'Negotiated x-api-version, e.g. 1.3-rev2. Persisted because the server accepts a request with the header omitted and silently falls back to a default revision — pinning it is what stops a VBR upgrade changing our response schema underneath us';
COMMENT ON COLUMN veeam_servers.product_version IS 'serverInfo.buildVersion, e.g. 13.1.0.411. Gates the 13.1 minimum: Proxmox jobs report type "Unknown" with no lastRun on 13.0.x';
COMMENT ON COLUMN veeam_servers.license_edition IS 'license.edition, e.g. EnterprisePlus. Proxmox workloads require Enterprise Plus; Community Edition cannot use this integration at all';
COMMENT ON COLUMN veeam_servers.tls_fingerprint IS 'SHA-256 of the leaf certificate, pinned at add-time. Empty means verify against the system CA pool — VBR ships self-signed, but a reverse-proxied deployment can present a publicly-valid chain, so both paths are real';
COMMENT ON COLUMN veeam_servers.last_sync_error IS 'Last collector failure for this server, surfaced in the UI. Empty when the last sync succeeded';

CREATE TRIGGER trg_veeam_servers_updated_at
    BEFORE UPDATE ON veeam_servers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Veeam's platformId identifies one Proxmox connection: it is identical across
-- every backup object, restore point and session originating from that cluster,
-- and it is the ONLY cluster discriminator the REST API exposes. The Proxmox VE
-- server is not registered under backupInfrastructure/managedServers, and
-- ProxmoxObjectModel (which carries clusterId/nodeId) is not reachable from any
-- path on 13.1 — so platform_id is what Nexara correlates on.
--
-- cluster_id is nullable and operator-confirmed: an unmapped platform is
-- visible only to holders of GLOBAL view:veeam, which is the fail-closed
-- posture standalone PBS servers already use.
CREATE TABLE IF NOT EXISTS veeam_platforms (
    veeam_server_id UUID NOT NULL REFERENCES veeam_servers(id) ON DELETE CASCADE,
    platform_id     UUID NOT NULL,
    display_name    TEXT NOT NULL DEFAULT '',
    cluster_id      UUID REFERENCES clusters(id) ON DELETE SET NULL,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (veeam_server_id, platform_id)
);

COMMENT ON TABLE veeam_platforms IS 'Maps a Veeam platformId (one Proxmox connection) to a Nexara cluster. The join that makes every other Veeam table cluster-scopable for RBAC';
COMMENT ON COLUMN veeam_platforms.display_name IS 'Human-readable label, sourced from the license workload hostName (e.g. the Proxmox cluster name). A soft correlation signal and the default UI label';
COMMENT ON COLUMN veeam_platforms.cluster_id IS 'NULL until an operator confirms the mapping. Rows with NULL are unattributable and require global view:veeam';

CREATE INDEX IF NOT EXISTS idx_veeam_platforms_cluster_id ON veeam_platforms (cluster_id);

-- Permission family, seeded per 000078. Granted to the built-in Admin and
-- Operator roles, plus any custom role that already holds the equivalent PBS
-- grant, so existing operator-style roles keep working across the upgrade with
-- no action. Viewer gets view only.
--
-- Note there is no execute:veeam here: job control lands in Phase 4 and gets
-- its own migration, so an upgrade that stops at this release cannot grant a
-- capability the code does not yet implement.
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view',   'veeam', 'View Veeam Backup & Replication servers, jobs, sessions and restore points'),
    (gen_random_uuid(), 'manage', 'veeam', 'Register and modify Veeam Backup & Replication servers'),
    (gen_random_uuid(), 'delete', 'veeam', 'Remove Veeam Backup & Replication servers')
ON CONFLICT (action, resource) DO NOTHING;

-- Built-in Admin gets all three.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'veeam'
ON CONFLICT DO NOTHING;

-- Built-in Operator gets all three, matching its PBS grants.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'veeam'
ON CONFLICT DO NOTHING;

-- Built-in Viewer gets read-only visibility.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource = 'veeam' AND action = 'view'
ON CONFLICT DO NOTHING;

-- Custom roles: mirror whatever they already hold for PBS, action for action.
-- A role with manage:pbs but not delete:pbs gets manage:veeam and not
-- delete:veeam — the grant follows the operator's existing intent rather than
-- widening it.
--
-- PBS is the only correct analogue. It is the other credential registry: the
-- one other place a role can register a remote server, store a secret against
-- it and cause an outbound connection. manage:backup is deliberately NOT
-- mirrored — it is a per-cluster data-plane grant over backup *jobs* (000016:
-- "Create, restore, manage backup jobs") and has never conferred any of those
-- three. Mirroring it would hand a globally-assigned "Backup Operator" role
-- the ability to store a Veeam administrator credential, which is a privilege
-- it does not have today.
INSERT INTO role_permissions (role_id, permission_id)
SELECT DISTINCT rp.role_id, vp.id
FROM permissions vp
JOIN permissions ep ON ep.action = vp.action AND ep.resource = 'pbs'
JOIN role_permissions rp ON rp.permission_id = ep.id
WHERE vp.resource = 'veeam'
ON CONFLICT DO NOTHING;
