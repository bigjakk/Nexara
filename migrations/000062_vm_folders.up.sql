-- VM folders for the "VMs & Templates" tree perspective. Folders are a
-- pure organisation construct (analogous to vCenter VM folders); they do
-- not affect Proxmox-side configuration. Each folder belongs to a single
-- cluster and may be nested under another folder in the same cluster.
CREATE TABLE IF NOT EXISTS vm_folders (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    parent_id  UUID REFERENCES vm_folders(id) ON DELETE CASCADE,
    name       TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- NULLS NOT DISTINCT (PG15+) lets us treat top-level folders (parent_id IS NULL)
    -- as a single namespace per cluster, so two roots with the same name conflict.
    UNIQUE NULLS NOT DISTINCT (cluster_id, parent_id, name)
);

CREATE INDEX IF NOT EXISTS idx_vm_folders_cluster ON vm_folders(cluster_id);
CREATE INDEX IF NOT EXISTS idx_vm_folders_parent ON vm_folders(parent_id);

-- A VM lives in at most one folder. Cascade-delete on either side cleans
-- up dangling assignments when a folder or VM is removed.
CREATE TABLE IF NOT EXISTS vm_folder_memberships (
    vm_id      UUID PRIMARY KEY REFERENCES vms(id) ON DELETE CASCADE,
    folder_id  UUID NOT NULL REFERENCES vm_folders(id) ON DELETE CASCADE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_vm_folder_memberships_folder ON vm_folder_memberships(folder_id);

-- RBAC permissions for the new resource. Idempotent against the seed in 000016.
INSERT INTO permissions (action, resource, description) VALUES
    ('view',   'vm_folder', 'View VM folders'),
    ('manage', 'vm_folder', 'Create, rename, delete VM folders and assign VMs')
ON CONFLICT (action, resource) DO NOTHING;

-- Grant new permissions to the built-in roles (idempotent — only inserts
-- rows that don't already exist).
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001', id FROM permissions
WHERE (action, resource) IN (('view', 'vm_folder'), ('manage', 'vm_folder'))
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002', id FROM permissions
WHERE (action, resource) IN (('view', 'vm_folder'), ('manage', 'vm_folder'))
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003', id FROM permissions
WHERE (action, resource) IN (('view', 'vm_folder'))
ON CONFLICT DO NOTHING;
