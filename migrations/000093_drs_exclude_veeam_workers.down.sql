-- 000093_drs_exclude_veeam_workers.down.sql
-- Reverse of 000093.
ALTER TABLE drs_configs DROP COLUMN IF EXISTS exclude_veeam_workers;
