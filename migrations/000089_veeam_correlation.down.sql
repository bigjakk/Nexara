-- 000089_veeam_correlation.down.sql
-- Reverse of 000089.
--
-- Schedules using the newly-allowed report type are removed before the
-- narrower constraint is restored, mirroring 000081's down.
DELETE FROM report_schedules WHERE report_type = 'veeam_backup_compliance';

ALTER TABLE report_schedules
  DROP CONSTRAINT IF EXISTS report_schedules_report_type_check;

ALTER TABLE report_schedules
  ADD CONSTRAINT report_schedules_report_type_check
  CHECK (report_type IN ('resource_utilization', 'capacity_forecast', 'backup_compliance', 'patch_status', 'uptime_summary', 'vm_resource_usage', 'snapshot_inventory'));

DROP INDEX IF EXISTS idx_veeam_backup_objects_guest;

ALTER TABLE veeam_backup_objects
    DROP CONSTRAINT IF EXISTS veeam_backup_objects_match_method_check;

ALTER TABLE veeam_backup_objects DROP COLUMN IF EXISTS match_method;
ALTER TABLE veeam_backup_objects DROP COLUMN IF EXISTS vmid;
ALTER TABLE veeam_backup_objects DROP COLUMN IF EXISTS cluster_id;

DROP TABLE IF EXISTS guest_smbios;
