-- Remove the console:* permission family and all grants of it. Reverting
-- re-anchors nothing by itself — the paired application release gates
-- console tokens on console:*, so run this only alongside a rollback to a
-- release that still gates them on view:*.

DELETE FROM role_permissions rp
USING permissions p
WHERE rp.permission_id = p.id AND p.action = 'console';

DELETE FROM permissions WHERE action = 'console';
