-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Lets a report schedule name the new rightsizing report.
--
-- report_schedules.report_type carries a CHECK listing the report types the
-- generator implements (last re-created by 000103). A type missing from it
-- makes saving that type's schedule fail with a constraint violation, which is
-- what 000081 had to repair for vm_resource_usage. This re-creates the CHECK
-- with 'rightsizing' added. Every existing row already satisfies the new list,
-- so nothing is rewritten. If interrupted, the transaction rolls back and the
-- old CHECK stays.

ALTER TABLE report_schedules
  DROP CONSTRAINT IF EXISTS report_schedules_report_type_check;

ALTER TABLE report_schedules
  ADD CONSTRAINT report_schedules_report_type_check
  CHECK (report_type IN ('resource_utilization', 'capacity_forecast', 'backup_compliance', 'patch_status', 'uptime_summary', 'vm_resource_usage', 'snapshot_inventory', 'cluster_digest', 'rightsizing'));
