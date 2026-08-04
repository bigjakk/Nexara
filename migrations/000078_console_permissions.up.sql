-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- Security fix: console access was gated on the generic view:* permissions.
-- The built-in Viewer role holds every view permission (000016), so a
-- read-only account could mint a console token for a root shell on a
-- Proxmox node (type=node_shell was gated on view:node). This migration
-- introduces a dedicated console:* permission family; the console-token
-- endpoint (internal/api/handlers/auth.go ConsoleToken) is re-anchored to
-- it in the same release.
--
-- Grant seeding is self-resolving: every role that holds manage:<resource>
-- receives console:<resource>. That covers the built-in Admin and Operator
-- roles plus any custom role with equivalent manage rights, so operator-style
-- roles keep console access across the upgrade with no action needed.
-- Roles without manage rights — including the built-in Viewer — lose console
-- access; that is the fix, not a side effect. Built-in roles are immutable,
-- so to give a view-only user consoles back, create a custom role with the
-- console:node / console:vm / console:container grants (Admin > Roles) and
-- assign it alongside Viewer.

INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'console', 'node',      'Open node shell consoles (root shell on the Proxmox host)'),
    (gen_random_uuid(), 'console', 'vm',        'Open VM serial and VNC consoles'),
    (gen_random_uuid(), 'console', 'container', 'Open container attach and VNC consoles')
ON CONFLICT (action, resource) DO NOTHING;

-- Built-in Admin gets all console permissions.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE action = 'console'
ON CONFLICT DO NOTHING;

-- Built-in Operator gets all console permissions.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE action = 'console'
ON CONFLICT DO NOTHING;

-- Custom roles: any role holding manage:<resource> keeps console access to
-- that resource class, so existing operator-style roles are unaffected by
-- the re-anchoring.
INSERT INTO role_permissions (role_id, permission_id)
SELECT rp.role_id, cp.id
FROM permissions cp
JOIN permissions mp ON mp.resource = cp.resource AND mp.action = 'manage'
JOIN role_permissions rp ON rp.permission_id = mp.id
WHERE cp.action = 'console'
ON CONFLICT DO NOTHING;
