-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000092_veeam_alert_metrics.up.sql
-- Adds a 'global' alert scope, for metrics that describe infrastructure no
-- single cluster owns.
--
-- Veeam repositories are the first such metric. One repository holds the
-- backups of every cluster a Veeam server protects, so "this repository is 94%
-- full" is not a fact about any one cluster — attributing it to one would be
-- wrong, and attributing it to all of them would raise the same alarm N times.
--
-- The RBAC this needs already works. A global rule raises alerts with a NULL
-- cluster_id, and ListAlertHistoryFiltered's `cluster_id = ANY(...)` is NULL
-- for a NULL cluster_id — so the alert ROW, with its repository name and
-- usage figure, is readable only by a holder of global view:alert. That is the
-- same fail-closed posture an unmapped Veeam platform takes.
--
-- The live "an alert fired" websocket event is a separate matter: a rule with
-- no cluster publishes on the system channel, which every authenticated
-- session may join, exactly as the Veeam sync's own events already do. It
-- carries an id and an action and no values, so what leaks is that something
-- fired and when — not what it said.
--
-- Same drop-and-readd shape as 000081's report_type expansion. Purely
-- additive: no existing row can violate the wider constraint.

ALTER TABLE alert_rules
  DROP CONSTRAINT IF EXISTS alert_rules_scope_type_check;

ALTER TABLE alert_rules
  ADD CONSTRAINT alert_rules_scope_type_check
  CHECK (scope_type IN ('cluster', 'node', 'vm', 'global'));
