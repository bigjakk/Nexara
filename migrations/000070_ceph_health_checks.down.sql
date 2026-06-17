-- 000070_ceph_health_checks.down.sql
ALTER TABLE ceph_cluster_metrics
    DROP COLUMN IF EXISTS health_checks;
