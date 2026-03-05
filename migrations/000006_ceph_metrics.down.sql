-- 000006_ceph_metrics.down.sql
-- Reverse Ceph metrics tables and aggregates

SELECT remove_retention_policy('ceph_pool_metrics', if_exists => true);
SELECT remove_retention_policy('ceph_osd_metrics', if_exists => true);
SELECT remove_retention_policy('ceph_cluster_metrics', if_exists => true);

SELECT remove_continuous_aggregate_policy('ceph_cluster_metrics_1h', if_not_exists => true);
SELECT remove_continuous_aggregate_policy('ceph_cluster_metrics_5m', if_not_exists => true);

DROP MATERIALIZED VIEW IF EXISTS ceph_cluster_metrics_1h;
DROP MATERIALIZED VIEW IF EXISTS ceph_cluster_metrics_5m;

DROP TABLE IF EXISTS ceph_pool_metrics;
DROP TABLE IF EXISTS ceph_osd_metrics;
DROP TABLE IF EXISTS ceph_cluster_metrics;
