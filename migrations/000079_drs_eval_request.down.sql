-- Drops the manual-evaluation queue slot. Any pending request is discarded;
-- the paired application release executes manual triggers in-process again.

ALTER TABLE drs_configs
    DROP COLUMN IF EXISTS eval_requested_at;
