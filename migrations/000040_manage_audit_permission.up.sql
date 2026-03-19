-- Add missing RBAC permissions
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'manage', 'audit', 'Manage audit configuration and syslog settings'),
    (gen_random_uuid(), 'manage', 'settings', 'Manage global application settings')
ON CONFLICT (action, resource) DO NOTHING;

-- Admin gets manage:audit
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'audit' AND action = 'manage'
ON CONFLICT DO NOTHING;

-- Operator gets manage:audit
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'audit' AND action = 'manage'
ON CONFLICT DO NOTHING;

-- Admin gets manage:settings
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'settings' AND action = 'manage'
ON CONFLICT DO NOTHING;
