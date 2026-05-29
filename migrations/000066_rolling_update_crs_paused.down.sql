ALTER TABLE rolling_update_jobs
    DROP COLUMN IF EXISTS native_crs_paused,
    DROP COLUMN IF EXISTS saved_crs_config;
