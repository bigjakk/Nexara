-- Remove the access:* permission family and every grant of it.
-- Purely additive in the up direction, so this is a clean revert: the paired
-- application release is the only thing that reads these rows.

DELETE FROM role_permissions rp
USING permissions p
WHERE rp.permission_id = p.id AND p.resource = 'access';

DELETE FROM permissions WHERE resource = 'access';
