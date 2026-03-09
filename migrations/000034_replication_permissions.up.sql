-- P9-T5: Replication Management permissions
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view', 'replication', 'View replication jobs and status'),
    (gen_random_uuid(), 'manage', 'replication', 'Create, update, delete, and trigger replication jobs')
ON CONFLICT (action, resource) DO NOTHING;

-- Admin gets all replication permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'replication'
ON CONFLICT DO NOTHING;

-- Operator gets all replication permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'replication'
ON CONFLICT DO NOTHING;

-- Viewer gets view only
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource = 'replication' AND action = 'view'
ON CONFLICT DO NOTHING;
