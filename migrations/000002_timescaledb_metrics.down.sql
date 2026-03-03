-- 000002_timescaledb_metrics.down.sql
-- Drop in reverse order

SELECT remove_retention_policy('vm_metrics', if_exists => true);
SELECT remove_retention_policy('node_metrics', if_exists => true);

DROP MATERIALIZED VIEW IF EXISTS vm_metrics_1h;
DROP MATERIALIZED VIEW IF EXISTS vm_metrics_5m;
DROP MATERIALIZED VIEW IF EXISTS node_metrics_1h;
DROP MATERIALIZED VIEW IF EXISTS node_metrics_5m;

DROP TABLE IF EXISTS vm_metrics;
DROP TABLE IF EXISTS node_metrics;
