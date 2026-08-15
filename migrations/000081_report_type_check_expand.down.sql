-- 000081_report_type_check_expand.down.sql
-- Remove schedules using the newly-allowed types before restoring the
-- original (narrower, vm_resource_usage-less) constraint, mirroring
-- 000023's down.
DELETE FROM report_schedules WHERE report_type IN ('vm_resource_usage', 'snapshot_inventory');

ALTER TABLE report_schedules
  DROP CONSTRAINT IF EXISTS report_schedules_report_type_check;

ALTER TABLE report_schedules
  ADD CONSTRAINT report_schedules_report_type_check
  CHECK (report_type IN ('resource_utilization', 'capacity_forecast', 'backup_compliance', 'patch_status', 'uptime_summary'));
