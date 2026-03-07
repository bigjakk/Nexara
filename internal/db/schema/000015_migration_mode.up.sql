-- 000015_migration_mode.up.sql
-- Add migration_mode and target_storage to migration_jobs for storage migration support.

ALTER TABLE migration_jobs
    ADD COLUMN IF NOT EXISTS migration_mode TEXT NOT NULL DEFAULT 'live',
    ADD COLUMN IF NOT EXISTS target_storage TEXT NOT NULL DEFAULT '';
