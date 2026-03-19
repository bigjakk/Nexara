DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE resource = 'api_key'
);
DELETE FROM permissions WHERE resource = 'api_key';
DROP TABLE IF EXISTS api_keys;
