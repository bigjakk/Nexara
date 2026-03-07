-- 000016_rbac.up.sql
-- RBAC: roles, permissions, role_permissions, user_roles

-- roles
CREATE TABLE IF NOT EXISTS roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    is_builtin  BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_roles_updated_at
    BEFORE UPDATE ON roles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- permissions
CREATE TABLE IF NOT EXISTS permissions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    action      TEXT NOT NULL,
    resource    TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    UNIQUE (action, resource)
);

-- role_permissions (many-to-many)
CREATE TABLE IF NOT EXISTS role_permissions (
    role_id       UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

-- user_roles (scoped assignment)
CREATE TABLE IF NOT EXISTS user_roles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id    UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    scope_type TEXT NOT NULL DEFAULT 'global' CHECK (scope_type IN ('global', 'cluster')),
    scope_id   UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_user_role_scope UNIQUE NULLS NOT DISTINCT (user_id, role_id, scope_type, scope_id)
);

CREATE INDEX IF NOT EXISTS idx_user_roles_user_id ON user_roles (user_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_role_id ON user_roles (role_id);

-- Seed built-in roles
INSERT INTO roles (id, name, description, is_builtin) VALUES
    ('a0000000-0000-0000-0000-000000000001', 'Admin', 'Full access to all resources and settings', true),
    ('a0000000-0000-0000-0000-000000000002', 'Operator', 'Manage all resources except user and role administration', true),
    ('a0000000-0000-0000-0000-000000000003', 'Viewer', 'Read-only access to all resources', true)
ON CONFLICT (name) DO NOTHING;

-- Seed permission catalog
INSERT INTO permissions (action, resource, description) VALUES
    ('view',    'cluster',    'View clusters'),
    ('manage',  'cluster',    'Create, update clusters'),
    ('delete',  'cluster',    'Delete clusters'),
    ('view',    'node',       'View nodes'),
    ('view',    'vm',         'View virtual machines'),
    ('manage',  'vm',         'Create, update VM configuration'),
    ('execute', 'vm',         'Start, stop, migrate, snapshot VMs'),
    ('delete',  'vm',         'Destroy virtual machines'),
    ('view',    'container',  'View containers'),
    ('manage',  'container',  'Create, update container configuration'),
    ('execute', 'container',  'Start, stop, migrate, snapshot containers'),
    ('delete',  'container',  'Destroy containers'),
    ('view',    'storage',    'View storage pools and content'),
    ('manage',  'storage',    'Create, update, upload to storage'),
    ('delete',  'storage',    'Delete storage pools and content'),
    ('view',    'backup',     'View backups and backup jobs'),
    ('manage',  'backup',     'Create, restore, manage backup jobs'),
    ('delete',  'backup',     'Delete backup snapshots'),
    ('view',    'network',    'View networks, firewall, SDN'),
    ('manage',  'network',    'Create, update networks, firewall rules, SDN'),
    ('delete',  'network',    'Delete networks, firewall rules, SDN resources'),
    ('view',    'drs',        'View DRS configuration and history'),
    ('manage',  'drs',        'Update DRS config, create/delete rules'),
    ('view',    'schedule',   'View scheduled tasks'),
    ('manage',  'schedule',   'Create, update, delete schedules'),
    ('view',    'migration',  'View migration plans'),
    ('manage',  'migration',  'Create, execute, cancel migrations'),
    ('view',    'audit',      'View audit log'),
    ('view',    'task',       'View task history'),
    ('manage',  'task',       'Create and manage tasks'),
    ('view',    'ceph',       'View Ceph status and configuration'),
    ('manage',  'ceph',       'Manage Ceph pools'),
    ('view',    'pbs',        'View PBS servers and data'),
    ('manage',  'pbs',        'Create, update PBS servers'),
    ('delete',  'pbs',        'Delete PBS servers'),
    ('view',    'user',       'View user accounts'),
    ('manage',  'user',       'Create, update, delete users'),
    ('view',    'role',       'View roles and permissions'),
    ('manage',  'role',       'Create, update, delete roles and assignments')
ON CONFLICT (action, resource) DO NOTHING;

-- Assign ALL permissions to Admin role
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001', id FROM permissions
ON CONFLICT DO NOTHING;

-- Assign all permissions EXCEPT user/role management to Operator
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002', id FROM permissions
WHERE NOT (resource IN ('user', 'role'))
ON CONFLICT DO NOTHING;

-- Assign only view permissions to Viewer
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003', id FROM permissions
WHERE action = 'view'
ON CONFLICT DO NOTHING;

-- Migrate existing users: admin -> Admin role (global), user -> Viewer role (global)
INSERT INTO user_roles (user_id, role_id, scope_type)
SELECT u.id, 'a0000000-0000-0000-0000-000000000001', 'global'
FROM users u
WHERE u.role = 'admin'
  AND u.id != '00000000-0000-0000-0000-000000000000'
ON CONFLICT DO NOTHING;

INSERT INTO user_roles (user_id, role_id, scope_type)
SELECT u.id, 'a0000000-0000-0000-0000-000000000003', 'global'
FROM users u
WHERE u.role = 'user'
  AND u.id != '00000000-0000-0000-0000-000000000000'
ON CONFLICT DO NOTHING;
