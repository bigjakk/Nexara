-- Remove the guest_tools:* permission family and every grant of it.
-- Purely additive in the up direction, so this is a clean revert: the paired
-- application release is the only thing that reads these rows.

DELETE FROM role_permissions rp
USING permissions p
WHERE rp.permission_id = p.id AND p.resource = 'guest_tools';

DELETE FROM permissions WHERE resource = 'guest_tools';
