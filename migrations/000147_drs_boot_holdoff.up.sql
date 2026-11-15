-- 000147_drs_boot_holdoff.up.sql
-- drs_configs.boot_holdoff_minutes: for this many minutes after a node boots,
-- DRS plans no move onto it or off it, so a cold-booted cluster is not
-- rebalanced while its guests are still starting. 0 turns it off.
--
-- Additive, with a default: safe for an in-place upgrade. Existing clusters get
-- the 10-minute hold-off.

ALTER TABLE drs_configs ADD COLUMN IF NOT EXISTS boot_holdoff_minutes INTEGER NOT NULL DEFAULT 10;

COMMENT ON COLUMN drs_configs.boot_holdoff_minutes IS 'Minutes after a node boots during which DRS plans no move onto or off it, judged from the node uptime Proxmox reports. 0 turns it off; a node whose uptime is unknown is not held';
