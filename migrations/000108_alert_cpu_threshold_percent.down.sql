-- 000108_alert_cpu_threshold_percent.down.sql
--
-- Puts every CPU rule's threshold back in the 0–1 form the release before this
-- one compares against, so each rule keeps its meaning under that engine (90 %
-- becomes 0.9). This is not a byte-for-byte inverse: a rule saved at 90 on the
-- old release could never fire there, and now fires at 90 %, which is what it
-- always asked for; and a threshold above 100 comes back above 1, which a
-- second up leaves alone. Which rules the up migration scaled is not recorded.
UPDATE alert_rules
SET threshold = (threshold::numeric / 100)::double precision
WHERE metric = 'cpu_usage';
