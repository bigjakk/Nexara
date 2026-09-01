-- 000099_guest_tools_reboot_required.down.sql
-- Reverse of 000099.
ALTER TABLE guest_tools_state DROP COLUMN IF EXISTS reboot_required;
