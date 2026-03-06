-- 000011_migrations.down.sql
DROP TRIGGER IF EXISTS trg_migration_jobs_updated_at ON migration_jobs;
DROP TABLE IF EXISTS migration_jobs;
