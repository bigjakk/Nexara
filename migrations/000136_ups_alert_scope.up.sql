-- 000136_ups_alert_scope.up.sql
-- Per-UPS alert rules (Phase 2c): a rule of scope 'ups' judges the one UPS
-- alert_rules.ups_id names, instead of every UPS as a global rule does, and
-- the alerts it raises name it (alert_history.ups_id).
--
-- Purely additive: two nullable columns; the scope CHECK widened, the same
-- drop-and-readd shape as 000092, which no existing row can violate; and a
-- CHECK tying ups_id to the ups scope, which every existing row meets (its
-- ups_id is NULL and its scope is not 'ups'). A rule goes with its UPS
-- (CASCADE), and its alerts with it, through alert_history.rule_id.

ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS ups_id UUID REFERENCES ups_devices(id) ON DELETE CASCADE;
ALTER TABLE alert_history ADD COLUMN IF NOT EXISTS ups_id UUID REFERENCES ups_devices(id) ON DELETE SET NULL;

COMMENT ON COLUMN alert_rules.ups_id IS 'The UPS a rule of scope ups judges; NULL for every other scope';
COMMENT ON COLUMN alert_history.ups_id IS 'The UPS a per-UPS rule raised the alert for; NULL for any other rule''s';

ALTER TABLE alert_rules
  DROP CONSTRAINT IF EXISTS alert_rules_scope_type_check;

ALTER TABLE alert_rules
  ADD CONSTRAINT alert_rules_scope_type_check
  CHECK (scope_type IN ('cluster', 'node', 'vm', 'global', 'ups'));

ALTER TABLE alert_rules
  DROP CONSTRAINT IF EXISTS alert_rules_ups_scope_check;

ALTER TABLE alert_rules
  ADD CONSTRAINT alert_rules_ups_scope_check
  CHECK ((scope_type = 'ups') = (ups_id IS NOT NULL));

-- For the cascade from a removed UPS, which would scan each table otherwise.
CREATE INDEX IF NOT EXISTS idx_alert_rules_ups ON alert_rules (ups_id) WHERE ups_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alert_history_ups ON alert_history (ups_id) WHERE ups_id IS NOT NULL;
