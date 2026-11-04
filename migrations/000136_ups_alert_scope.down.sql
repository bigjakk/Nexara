-- 000136_ups_alert_scope.down.sql
-- Remove the per-UPS rules before restoring the narrower constraint, as
-- 000092's down removes the global ones; their alerts go with them
-- (alert_history.rule_id cascades).
DROP INDEX IF EXISTS idx_alert_history_ups;
DROP INDEX IF EXISTS idx_alert_rules_ups;

DELETE FROM alert_rules WHERE scope_type = 'ups';

ALTER TABLE alert_rules
  DROP CONSTRAINT IF EXISTS alert_rules_ups_scope_check;

ALTER TABLE alert_rules
  DROP CONSTRAINT IF EXISTS alert_rules_scope_type_check;

ALTER TABLE alert_rules
  ADD CONSTRAINT alert_rules_scope_type_check
  CHECK (scope_type IN ('cluster', 'node', 'vm', 'global'));

ALTER TABLE alert_history DROP COLUMN IF EXISTS ups_id;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS ups_id;
