-- APT Repository Management permissions
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view', 'apt_repository', 'View configured APT repositories on nodes'),
    (gen_random_uuid(), 'manage', 'apt_repository', 'Enable/disable or add standard APT repositories on nodes')
ON CONFLICT (action, resource) DO NOTHING;

-- Admin gets all apt_repository permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'apt_repository'
ON CONFLICT DO NOTHING;

-- Operator gets all apt_repository permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'apt_repository'
ON CONFLICT DO NOTHING;

-- Viewer gets view only
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource = 'apt_repository' AND action = 'view'
ON CONFLICT DO NOTHING;
