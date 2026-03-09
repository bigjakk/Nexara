DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE resource = 'certificate'
);
DELETE FROM permissions WHERE resource = 'certificate';
