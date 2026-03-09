-- P9-T4: Resource Pool permissions
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view', 'pool', 'View resource pools and members'),
    (gen_random_uuid(), 'manage', 'pool', 'Create, update, and delete resource pools')
ON CONFLICT (action, resource) DO NOTHING;

-- Admin gets all pool permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'pool'
ON CONFLICT DO NOTHING;

-- Operator gets all pool permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'pool'
ON CONFLICT DO NOTHING;

-- Viewer gets view only
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource = 'pool' AND action = 'view'
ON CONFLICT DO NOTHING;
