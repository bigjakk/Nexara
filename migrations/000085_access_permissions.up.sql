-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- Adds the view:access / manage:access permission pair, gating the new
-- Proxmox access-control feature (managing a cluster's PVE users, API tokens,
-- groups, roles and ACLs from Nexara).
--
-- Note the deliberately asymmetric grant: Admin gets both, Operator and Viewer
-- get view only.
--
-- manage:access can mint a PVE API token with the Administrator role, which is
-- full control of the cluster AND a way out of Nexara's own RBAC entirely — the
-- holder could take the minted token and drive Proxmox directly, with no Nexara
-- permission check in the path. Handing that to Operator by default would make
-- Operator silently equivalent to Admin. 000078_console_permissions is the
-- precedent: a capability that escalates past the role boundary gets carved out
-- rather than following the usual all-three-roles grant pattern.
--
-- view:access is safe for Viewer and Operator: it lists who holds access to a
-- cluster and never exposes a secret. Proxmox returns token secrets exactly
-- once, at creation, and the handler layer keeps them out of both API reads and
-- audit rows.
--
-- To give a non-Admin account management rights, create a custom role with
-- manage:access (Admin > Roles) and assign it deliberately.

INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view',   'access', 'View Proxmox users, API tokens, groups, roles and ACLs'),
    (gen_random_uuid(), 'manage', 'access', 'Create, modify and delete Proxmox users, API tokens, groups, roles and ACLs')
ON CONFLICT (action, resource) DO NOTHING;

-- Built-in Admin gets both.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'access'
ON CONFLICT DO NOTHING;

-- Built-in Operator gets read-only visibility.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'access' AND action = 'view'
ON CONFLICT DO NOTHING;

-- Built-in Viewer gets read-only visibility.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource = 'access' AND action = 'view'
ON CONFLICT DO NOTHING;
