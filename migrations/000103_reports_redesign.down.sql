-- 000103_reports_redesign.down.sql
-- Restore the 000089 CHECK. cluster_digest schedules cannot survive the
-- narrower constraint, so they are removed, as 000089's down does for its own
-- addition.

DELETE FROM report_schedules WHERE report_type = 'cluster_digest';

ALTER TABLE report_schedules
  DROP CONSTRAINT IF EXISTS report_schedules_report_type_check;

ALTER TABLE report_schedules
  ADD CONSTRAINT report_schedules_report_type_check
  CHECK (report_type IN ('resource_utilization', 'capacity_forecast', 'backup_compliance', 'patch_status', 'uptime_summary', 'vm_resource_usage', 'snapshot_inventory', 'veeam_backup_compliance'));

ALTER TABLE report_runs DROP COLUMN IF EXISTS parameters;

ALTER TABLE report_schedules DROP COLUMN IF EXISTS run_as;
