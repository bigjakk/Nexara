-- Reverses 000134: execute:ups and its grants, the run record, the account's
-- verified mark, and the command and writable-variable lists.
DELETE FROM role_permissions rp
USING permissions p
WHERE rp.permission_id = p.id AND p.resource = 'ups' AND p.action = 'execute';

DELETE FROM permissions WHERE resource = 'ups' AND action = 'execute';

DROP TABLE IF EXISTS ups_commands;

ALTER TABLE nut_servers DROP COLUMN IF EXISTS commands_verified_at;

ALTER TABLE ups_devices DROP COLUMN IF EXISTS commands_driver;
ALTER TABLE ups_devices DROP COLUMN IF EXISTS commands_listed_at;
ALTER TABLE ups_devices DROP COLUMN IF EXISTS rw_vars;
ALTER TABLE ups_devices DROP COLUMN IF EXISTS commands;
