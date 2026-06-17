-- 000070_ceph_health_checks.up.sql
-- Capture the individual Ceph health checks (the reasons behind HEALTH_WARN /
-- HEALTH_ERR, e.g. MON_DISK_LOW, OSD_NEARFULL, RECENT_CRASH) alongside the
-- top-level status so the UI can show *why* a cluster is unhealthy instead of
-- just "HEALTH_WARN".
--
-- Additive + nullable: existing rows get NULL, the collector backfills the
-- column on its next sync. Safe for in-place upgrade.
ALTER TABLE ceph_cluster_metrics
    ADD COLUMN IF NOT EXISTS health_checks JSONB;
