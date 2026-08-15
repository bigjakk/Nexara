-- 000081_report_type_check_expand.up.sql
-- Expand report_schedules' report_type CHECK to the full set of report types.
--
-- Two values were missing: snapshot_inventory (new in this release) and
-- vm_resource_usage, which shipped alongside the original constraint in
-- 000024 but was never added to it — scheduling that report type has failed
-- with a 500 since day one (ad-hoc runs were unaffected; report_runs has no
-- CHECK). Same drop-and-readd shape as 000023's channel_type expansion.
--
-- Automatic in-place upgrade — no manual steps, no operator action required.

ALTER TABLE report_schedules
  DROP CONSTRAINT IF EXISTS report_schedules_report_type_check;

ALTER TABLE report_schedules
  ADD CONSTRAINT report_schedules_report_type_check
  CHECK (report_type IN ('resource_utilization', 'capacity_forecast', 'backup_compliance', 'patch_status', 'uptime_summary', 'vm_resource_usage', 'snapshot_inventory'));
