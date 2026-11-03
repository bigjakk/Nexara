-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000134_ups_commands.up.sql
-- UPS commands and settings (Phase 2b): the commands and writable variables
-- the poller reads for each UPS, a record of every command run and variable
-- set through Nexara, and execute:ups.
--
-- Purely additive: columns with NOT NULL defaults PostgreSQL fills for
-- existing rows, two nullable timestamps, a new table, and a permission seeded
-- ON CONFLICT DO NOTHING.
--
-- Built-in roles cannot be edited, so every Operator can run every UPS command
-- after this upgrade, including one that switches a UPS's output off; to keep
-- someone from that, give them a custom role.

ALTER TABLE ups_devices ADD COLUMN IF NOT EXISTS commands JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE ups_devices ADD COLUMN IF NOT EXISTS rw_vars JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE ups_devices ADD COLUMN IF NOT EXISTS commands_listed_at TIMESTAMPTZ;
ALTER TABLE ups_devices ADD COLUMN IF NOT EXISTS commands_driver TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN ups_devices.commands IS 'LIST CMD, with each command''s GET CMDDESC text once read: [{"name", "description"}]';
COMMENT ON COLUMN ups_devices.rw_vars IS 'LIST RW, with each variable''s GET TYPE, LIST ENUM and LIST RANGE once read: {name: {"type", "max_len", "enum", "ranges"}}. type is "" until read';
COMMENT ON COLUMN ups_devices.commands_listed_at IS 'When the poller last listed commands and rw_vars. NULL until the first listing, and until then nothing can be run';
COMMENT ON COLUMN ups_devices.commands_driver IS 'The driver, name and version, the lists were read under: a different one, seen after a restart too, reads them all again';

ALTER TABLE nut_servers ADD COLUMN IF NOT EXISTS commands_verified_at TIMESTAMPTZ;

COMMENT ON COLUMN nut_servers.commands_verified_at IS 'When upsd last took a command or variable set from the stored account. upsd checks an account only when a command runs, so this is the only proof it may run them; cleared when upsd refuses it, and when the address, username or password changes';

CREATE TABLE IF NOT EXISTS ups_commands (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ups_id       UUID NOT NULL REFERENCES ups_devices(id) ON DELETE CASCADE,
    user_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('command', 'set')),
    name         TEXT NOT NULL,
    argument     TEXT NOT NULL DEFAULT '',
    previous     TEXT,
    status       TEXT NOT NULL DEFAULT 'sent'
                 CHECK (status IN ('sent', 'pending', 'succeeded', 'failed', 'unconfirmed')),
    tracking_id  TEXT NOT NULL DEFAULT '',
    result       TEXT NOT NULL DEFAULT '',
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ
);

COMMENT ON TABLE ups_commands IS 'Every instant command run and variable set through Nexara, with the outcome the driver reported';
COMMENT ON COLUMN ups_commands.name IS 'The instant command (kind command) or the variable (kind set)';
COMMENT ON COLUMN ups_commands.argument IS 'The command''s parameter, or the value set. Never a credential';
COMMENT ON COLUMN ups_commands.previous IS 'For kind set, the value Nexara last read before the change';
COMMENT ON COLUMN ups_commands.status IS 'sent while Nexara talks to upsd; pending once upsd tracks it; succeeded or failed by the driver''s answer, or upsd''s refusal; unconfirmed when upsd took it but no answer came (NUT 2.7 has no tracking, the driver stayed pending for 60 s, the connection dropped, or Nexara stopped)';
COMMENT ON COLUMN ups_commands.result IS 'What came back, in words for the UI: why a run failed, or what a variable reads after it';

CREATE INDEX IF NOT EXISTS idx_ups_commands_ups_requested ON ups_commands (ups_id, requested_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_ups_commands_unfinished ON ups_commands (requested_at) WHERE status IN ('sent', 'pending');

-- execute:ups. 000131 left it out so that an upgrade stopping there could not
-- grant a capability the code did not have yet.
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'execute', 'ups', 'Run instant commands on UPS devices, including ones that switch off the power a UPS supplies')
ON CONFLICT (action, resource) DO NOTHING;

-- Built-in Admin and Operator only. Viewer and custom roles get none: a
-- command can cut the power to everything a UPS feeds, so a role is given it
-- on purpose.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.role_id, p.id
FROM permissions p
CROSS JOIN (VALUES ('a0000000-0000-0000-0000-000000000001'::uuid),
                   ('a0000000-0000-0000-0000-000000000002'::uuid)) AS r(role_id)
WHERE p.resource = 'ups' AND p.action = 'execute'
ON CONFLICT DO NOTHING;
