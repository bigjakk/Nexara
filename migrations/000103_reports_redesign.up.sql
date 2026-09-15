-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000103_reports_redesign.up.sql
-- The reports redesign: one backup compliance report across PBS and Veeam,
-- a new cluster digest type, and per-run parameters.
--
-- 1. report_schedules.report_type gains 'cluster_digest' and loses
--    'veeam_backup_compliance'. 000089 reserved the latter for a separate
--    multi-provider report; no generator, validator or UI ever implemented
--    it, so the API has always rejected it and no schedule can hold it
--    through Nexara. The multi-provider view ships as the upgraded
--    backup_compliance instead. Any row that reached the value some other
--    way is folded into backup_compliance first, so the re-added CHECK can
--    never fail on existing data.
-- 2. report_runs.parameters records the options a run was generated with,
--    so "run again" reproduces it and the viewer can show them. Existing
--    rows get the empty object, which the generator reads as the defaults
--    those runs were produced under.
-- 3. report_schedules.run_as names the user whose grants a scheduled run
--    reads under. That used to be the creator implicitly, which let anyone
--    holding manage:report edit a schedule into a report type, or onto a
--    cluster, where the creator's grants (view:veeam in particular) reach
--    further than their own. Every save now stamps the saver. Existing rows
--    take their creator, which is exactly what they ran as before.

UPDATE report_schedules
SET report_type = 'backup_compliance'
WHERE report_type = 'veeam_backup_compliance';

ALTER TABLE report_schedules
  DROP CONSTRAINT IF EXISTS report_schedules_report_type_check;

ALTER TABLE report_schedules
  ADD CONSTRAINT report_schedules_report_type_check
  CHECK (report_type IN ('resource_utilization', 'capacity_forecast', 'backup_compliance', 'patch_status', 'uptime_summary', 'vm_resource_usage', 'snapshot_inventory', 'cluster_digest'));

ALTER TABLE report_runs
  ADD COLUMN IF NOT EXISTS parameters JSONB NOT NULL DEFAULT '{}';

COMMENT ON COLUMN report_runs.parameters IS 'The report parameters this run was generated with (stale_after_hours, top_n, section toggles). Empty object = the defaults';

ALTER TABLE report_schedules
  ADD COLUMN IF NOT EXISTS run_as UUID REFERENCES users(id);

UPDATE report_schedules SET run_as = created_by WHERE run_as IS NULL;

ALTER TABLE report_schedules ALTER COLUMN run_as SET NOT NULL;

COMMENT ON COLUMN report_schedules.run_as IS 'The user whose grants each scheduled run reads under — the last person to save the schedule, never merely its creator. A run also refuses to start when this user is deactivated';
