-- 000108_alert_cpu_threshold_percent.up.sql
--
-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Rewrites the threshold of CPU alert rules saved at 1 or less; it matches the
-- "data backfill" review trigger and resolves it itself, in this transaction.
--
-- Why. The rule form asks for "CPU Usage (%)" (default 90), but the alert
-- engine compared that threshold with the stored figure, PVE's 0–1 ratio, so a
-- percent rule could never fire. The engine now scales the reading by 100.
--
-- What changes, rule by rule:
--   threshold <= 1  scaled by 100 with it (0.9 -> 90, 0.05 -> 5). Reading and
--                   threshold are both 100 times larger, so the rule fires
--                   when it fired before, whatever it was meant to say.
--   threshold > 1   left alone: a percentage the old engine could almost never
--                   reach (a VM's ratio only just passes 1), which now fires
--                   as written.
-- Only cpu_usage rules: every other metric keeps its own unit (a failed-backup
-- count of 1 must stay 1). numeric keeps the scaling exact (0.07 -> 7, not
-- 7.000000000000001). Interrupted: one atomic statement, so it ran or it did not.
--
-- Release note: CPU alert rules now fire at their percentage. Rules saved as a
-- fraction (0.9) now read as the percentage they meant (90).
UPDATE alert_rules
SET threshold = (threshold::numeric * 100)::double precision
WHERE metric = 'cpu_usage' AND threshold <= 1;
