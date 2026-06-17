-- 000071_health_signals.down.sql
DROP TABLE IF EXISTS replication_jobs;
ALTER TABLE vms      DROP COLUMN IF EXISTS lock_state;
ALTER TABLE nodes    DROP COLUMN IF EXISTS rootfs_used;
ALTER TABLE clusters DROP COLUMN IF EXISTS quorate;
