-- Schedules for the rightsizing report cannot survive the narrower CHECK, so
-- they are deleted first. Its runs stay: report_runs has no CHECK on its type.
DELETE FROM report_schedules WHERE report_type = 'rightsizing';

ALTER TABLE report_schedules
  DROP CONSTRAINT IF EXISTS report_schedules_report_type_check;

ALTER TABLE report_schedules
  ADD CONSTRAINT report_schedules_report_type_check
  CHECK (report_type IN ('resource_utilization', 'capacity_forecast', 'backup_compliance', 'patch_status', 'uptime_summary', 'vm_resource_usage', 'snapshot_inventory', 'cluster_digest'));
