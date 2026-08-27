-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000093_drs_exclude_veeam_workers.up.sql
-- Keeps DRS off Veeam's worker appliances.
--
-- Defaults to TRUE, which is a behaviour change for existing installs — a
-- deliberate one, and the safe direction. DRS treating a worker appliance as
-- ordinary load migrates it mid-backup, and the job running on it fails; that
-- is not a balance an operator asked for, and it leaves nothing behind but a
-- failed session. An operator who genuinely wants workers balanced can turn
-- this off.
--
-- SCOPE, stated so nobody reads more into the flag than it does: this pins the
-- guests Veeam OWNS. It does not stop DRS moving a PROTECTED guest away from
-- the node its worker happens to be on, which silently downgrades that guest's
-- transport from hot-add to network mode — no error, just a backup several
-- times slower. Covering that would mean pinning half the estate to wherever
-- Veeam last placed a worker, and Veeam re-places them per job run.
--
-- Purely additive — one column with a DEFAULT.

ALTER TABLE drs_configs
    ADD COLUMN IF NOT EXISTS exclude_veeam_workers BOOLEAN NOT NULL DEFAULT true;

COMMENT ON COLUMN drs_configs.exclude_veeam_workers IS 'Pin the guests Veeam owns — worker appliances and, when it runs on the cluster it protects, the VBR server — so DRS never selects them for migration. They are still SCORED, exactly like a container under include_containers=false: their load is real and a node carrying three of them is genuinely busier';
