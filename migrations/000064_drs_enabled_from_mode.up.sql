-- 000064_drs_enabled_from_mode.up.sql
-- The DRS UI no longer exposes a separate Enabled toggle; the Mode dropdown
-- alone controls user intent (disabled vs advisory vs automatic). Backfill
-- existing rows so that any cluster whose mode is not 'disabled' is enabled,
-- and any cluster whose mode is 'disabled' is disabled. The `enabled` column
-- remains as a runtime pause flag used by the rolling-update orchestrator.
UPDATE drs_configs SET enabled = (mode != 'disabled');
