DROP INDEX IF EXISTS idx_rolling_update_jobs_cleanup;

ALTER TABLE rolling_update_nodes
    DROP COLUMN IF EXISTS stopped_passthrough_json;

ALTER TABLE rolling_update_jobs
    DROP COLUMN IF EXISTS cleanup_attempts,
    DROP COLUMN IF EXISTS cleanup_pending,
    DROP COLUMN IF EXISTS disabled_ha_rules;
