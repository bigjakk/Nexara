-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000098_guest_tools_permissions.up.sql
-- Permissions for Windows guest tools version tracking and staged updates.
--
-- A dedicated resource rather than reusing execute:vm. The two are close —
-- anyone holding execute:vm can already change a guest's boot media, which is
-- effectively full control of the guest — but "run this installer inside the
-- operating system" is worth being able to grant and audit on its own, and
-- worth being able to withhold from someone trusted to reboot a VM.
--
-- The grant split mirrors 000085: Admin gets everything, Operator and Viewer
-- get visibility. Staging an update is deliberately Admin-only to start with;
-- it replaces storage and network drivers inside a running Windows guest, and
-- that is not a privilege to hand out by default. An operator who wants it can
-- add execute:guest_tools to a custom role.
--
-- NOTE ON UPGRADE: already-signed-in users keep a cached permission set for up
-- to five minutes (nexara:rbac:<user>, TTL 5m, and InvalidateUser is a
-- single-user DEL with no bulk flush), so a brand-new permission can 403 for
-- that long after this migration applies. Nothing to do — it resolves itself.

INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view',    'guest_tools', 'View installed guest tools versions and update status for Windows guests'),
    (gen_random_uuid(), 'manage',  'guest_tools', 'Configure guest tools update policy, pinned versions and exclusions'),
    (gen_random_uuid(), 'execute', 'guest_tools', 'Stage and run guest tools updates inside Windows guests')
ON CONFLICT (action, resource) DO NOTHING;

-- Built-in Admin gets all three.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'guest_tools'
ON CONFLICT DO NOTHING;

-- Built-in Operator gets read-only visibility.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'guest_tools' AND action = 'view'
ON CONFLICT DO NOTHING;

-- Built-in Viewer gets read-only visibility.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource = 'guest_tools' AND action = 'view'
ON CONFLICT DO NOTHING;
