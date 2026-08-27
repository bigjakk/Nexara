-- 000092_veeam_alert_metrics.down.sql
-- Remove rules using the newly-allowed scope before restoring the narrower
-- constraint, mirroring 000081's down.
DELETE FROM alert_rules WHERE scope_type = 'global';

ALTER TABLE alert_rules
  DROP CONSTRAINT IF EXISTS alert_rules_scope_type_check;

ALTER TABLE alert_rules
  ADD CONSTRAINT alert_rules_scope_type_check
  CHECK (scope_type IN ('cluster', 'node', 'vm'));
