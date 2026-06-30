-- Import VM jobs: tracks each guest imported into Proxmox from a foreign source
-- (an OVA/OVF appliance, a raw disk image, or an ESXi/vCenter guest) via Proxmox's
-- create-with-import-from flow. One row per imported guest; the long-running disk
-- conversion is a Proxmox task whose UPID is stored here and reconciled to a terminal
-- status by the scheduler. Purely additive — safe for in-place upgrade.
CREATE TABLE IF NOT EXISTS vm_import_jobs (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id         UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    -- How the source arrives, kept orthogonal to its format so a URL-fetched OVA is
    -- representable (acquisition='url', format='ova').
    source_acquisition TEXT NOT NULL DEFAULT 'staged'
        CHECK (source_acquisition IN ('staged', 'url', 'esxi', 'upload')),
    source_format      TEXT NOT NULL DEFAULT '',
    source_ref         TEXT NOT NULL DEFAULT '',
    target_node        TEXT NOT NULL,
    target_storage     TEXT NOT NULL,
    target_vmid        INT  NOT NULL,
    name               TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    upid               TEXT NOT NULL DEFAULT '',
    failure_reason     TEXT NOT NULL DEFAULT '',
    warnings_json      JSONB NOT NULL DEFAULT '[]',
    options_json       JSONB NOT NULL DEFAULT '{}',
    created_by         UUID NOT NULL REFERENCES users(id),
    started_at         TIMESTAMPTZ,
    completed_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_vm_import_jobs_cluster ON vm_import_jobs(cluster_id);
CREATE INDEX IF NOT EXISTS idx_vm_import_jobs_status ON vm_import_jobs(status);

CREATE TRIGGER trg_vm_import_jobs_updated_at
    BEFORE UPDATE ON vm_import_jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- RBAC permissions for the new resource. Idempotent against the seed in 000016.
INSERT INTO permissions (action, resource, description) VALUES
    ('view',   'vm_import', 'View VM import jobs'),
    ('manage', 'vm_import', 'Start, cancel, and manage VM imports and import sources')
ON CONFLICT (action, resource) DO NOTHING;

-- Grant to built-in roles: view to Admin/Operator/Viewer, manage to Admin/Operator.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001', id FROM permissions
WHERE (action, resource) IN (('view', 'vm_import'), ('manage', 'vm_import'))
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002', id FROM permissions
WHERE (action, resource) IN (('view', 'vm_import'), ('manage', 'vm_import'))
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003', id FROM permissions
WHERE (action, resource) IN (('view', 'vm_import'))
ON CONFLICT DO NOTHING;
