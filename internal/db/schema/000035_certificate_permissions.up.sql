-- P9-T6: ACME Certificate Management permissions
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view', 'certificate', 'View ACME accounts, plugins, and node certificates'),
    (gen_random_uuid(), 'manage', 'certificate', 'Manage ACME accounts, plugins, and order/renew/revoke certificates')
ON CONFLICT (action, resource) DO NOTHING;

-- Admin gets all certificate permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'certificate'
ON CONFLICT DO NOTHING;

-- Operator gets view only (manage is Admin-only per spec)
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'certificate' AND action = 'view'
ON CONFLICT DO NOTHING;

-- Viewer gets view only
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource = 'certificate' AND action = 'view'
ON CONFLICT DO NOTHING;
