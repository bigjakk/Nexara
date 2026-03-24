-- Remove manage:node role assignments
DELETE FROM role_permissions
WHERE permission_id IN (SELECT id FROM permissions WHERE action = 'manage' AND resource = 'node');

-- Remove manage:node permission
DELETE FROM permissions WHERE action = 'manage' AND resource = 'node';
