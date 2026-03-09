-- P9-T2: HA Management permissions
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view', 'ha', 'View HA resources, groups, and status'),
    (gen_random_uuid(), 'manage', 'ha', 'Create, update, and delete HA resources and groups')
ON CONFLICT (action, resource) DO NOTHING;

-- Admin gets all HA permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'ha'
ON CONFLICT DO NOTHING;

-- Operator gets all HA permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'ha'
ON CONFLICT DO NOTHING;

-- Viewer gets view only
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource = 'ha' AND action = 'view'
ON CONFLICT DO NOTHING;
