DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE (resource = 'audit' AND action = 'manage')
        OR (resource = 'settings' AND action = 'manage')
);
DELETE FROM permissions WHERE (resource = 'audit' AND action = 'manage')
    OR (resource = 'settings' AND action = 'manage');
