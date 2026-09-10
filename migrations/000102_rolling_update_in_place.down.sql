ALTER TABLE rolling_update_nodes DROP COLUMN IF EXISTS reboot_required;
ALTER TABLE rolling_update_jobs  DROP COLUMN IF EXISTS drain_guests;
