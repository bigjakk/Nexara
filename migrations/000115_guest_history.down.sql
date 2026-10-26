DROP TRIGGER IF EXISTS trg_vms_status_change ON vms;
DROP FUNCTION IF EXISTS record_guest_status_change();
DROP TABLE IF EXISTS guest_status_changes;
DROP TABLE IF EXISTS task_history_archive;
