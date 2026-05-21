-- 000064_drs_enabled_from_mode.down.sql
-- No-op: this migration only backfills data and is safe to re-apply. There is
-- no meaningful inverse since prior user intent (the separate Enabled toggle)
-- is no longer captured.
SELECT 1;
