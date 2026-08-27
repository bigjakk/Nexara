-- 000091_veeam_manual_pin_key.down.sql
-- Reverse of 000091.
ALTER TABLE veeam_backup_objects DROP COLUMN IF EXISTS manual_guest_key;
