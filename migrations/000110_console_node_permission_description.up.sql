-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Rewords one seeded permission description; no grant changes.
--
-- 000078 seeded console:node as "Open node shell consoles (root shell on the
-- Proxmox host)". That overstates it: Proxmox hands an automatic root shell
-- only to root@pam, and Nexara opens node consoles with an API token, which
-- always gets /bin/login (pve-manager Nodes.pm get_shell_command). The
-- operator still has to sign in to the node. The description is what
-- GET /api/v1/rbac/permissions returns for the permission, and what the
-- admin guide lists, so it must not promise root.
--
-- 000078 is released and is not edited. Every role keeps exactly the access
-- it had.

UPDATE permissions
SET description = 'Open node shell consoles (a login prompt on the Proxmox host)'
WHERE action = 'console' AND resource = 'node';
