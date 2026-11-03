-- UPS monitoring, Phase 2a: what the "not protected" check needs beyond 000132.
--
-- Purely additive: one nullable column and one with a default, on tables
-- 000131 and 000132 created, so an upgrade needs no operator action.

ALTER TABLE ups_devices ADD COLUMN IF NOT EXISTS clients_listed_at TIMESTAMPTZ;

COMMENT ON COLUMN ups_devices.clients_listed_at IS 'When the poller last listed who is logged in to the UPS, which it does only while the UPS is read on mains. NULL until the first listing; until then no node the UPS powers has a verdict';

ALTER TABLE ups_feeds ADD COLUMN IF NOT EXISTS protected_elsewhere BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN ups_feeds.protected_elsewhere IS 'The operator says the node shuts down some way Nexara cannot see: its upsmon is behind NAT or a proxy, or another tool runs the shutdown. Its verdict reads checked elsewhere';
