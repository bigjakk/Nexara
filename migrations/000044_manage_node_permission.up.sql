-- Add manage:node permission (was missing — only view:node existed)
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'manage', 'node', 'Manage node settings, evacuate, reboot, shutdown')
ON CONFLICT (action, resource) DO NOTHING;

-- Admin gets manage:node
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'node' AND action = 'manage'
ON CONFLICT DO NOTHING;

-- Operator gets manage:node
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'node' AND action = 'manage'
ON CONFLICT DO NOTHING;
