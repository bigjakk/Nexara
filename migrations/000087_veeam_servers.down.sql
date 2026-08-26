-- Reverse 000087_veeam_servers.
--
-- Drops the Veeam server registry, the platform → cluster mapping, and the
-- view/manage/delete:veeam permission family along with every grant of it.
-- Registered Veeam servers and their encrypted credentials are removed; the
-- Veeam side is untouched (Nexara never wrote to it in Phase 1), so recovery
-- is re-adding the server through the UI.
--
-- veeam_platforms is dropped explicitly rather than relying on the CASCADE
-- from veeam_servers, so the order here reads the same as the up migration in
-- reverse. The updated_at trigger goes with its table; naming it separately
-- would be a statement that errors rather than no-ops if this ever ran twice,
-- because DROP TRIGGER IF EXISTS still requires the table to exist.

DROP TABLE IF EXISTS veeam_platforms;

DROP TABLE IF EXISTS veeam_servers;

DELETE FROM role_permissions rp
USING permissions p
WHERE rp.permission_id = p.id AND p.resource = 'veeam';

DELETE FROM permissions WHERE resource = 'veeam';
