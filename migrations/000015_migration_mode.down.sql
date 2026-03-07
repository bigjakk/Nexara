-- 000015_migration_mode.down.sql

ALTER TABLE migration_jobs
    DROP COLUMN IF EXISTS migration_mode,
    DROP COLUMN IF EXISTS target_storage;
