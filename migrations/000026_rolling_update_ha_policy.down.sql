ALTER TABLE rolling_update_jobs
  DROP COLUMN IF EXISTS ha_warnings,
  DROP COLUMN IF EXISTS ha_policy;
