-- Reverse 000131_nut_ups.
--
-- Drops the NUT server registry, every UPS device, its readings, rollups and
-- power events, and the view/manage/delete:ups permission family with every
-- grant of it. Nothing outside Nexara is touched (Phase 1 never writes to
-- upsd), so recovery is re-adding the servers through the UI; the readings
-- and events collected so far are gone.
--
-- The policies go first, before this transaction locks any relation. A policy
-- job that is running holds its job lock and wants the hypertable; dropping a
-- view first would hold the hypertable and then want the job lock, and the
-- two deadlock (seen right after an up, when the first refresh is under way).
-- Then the aggregates before their hypertable, and the tables leaf-first, so
-- the order reads as the up migration in reverse. Triggers go with their tables.

-- Each guarded by to_regclass, so a second run (after a `migrate force`) is a
-- no-op rather than a failed cast to a relation that is already gone.
SELECT remove_continuous_aggregate_policy(to_regclass('ups_metrics_1h'), if_not_exists => true)
WHERE to_regclass('ups_metrics_1h') IS NOT NULL;
SELECT remove_continuous_aggregate_policy(to_regclass('ups_metrics_5m'), if_not_exists => true)
WHERE to_regclass('ups_metrics_5m') IS NOT NULL;
SELECT remove_retention_policy(to_regclass('ups_metrics'), if_exists => true)
WHERE to_regclass('ups_metrics') IS NOT NULL;

DROP MATERIALIZED VIEW IF EXISTS ups_metrics_1h;
DROP MATERIALIZED VIEW IF EXISTS ups_metrics_5m;
DROP TABLE IF EXISTS ups_metrics;
DROP TABLE IF EXISTS ups_events;
DROP TABLE IF EXISTS ups_devices;
DROP TABLE IF EXISTS nut_servers;

DELETE FROM role_permissions rp
USING permissions p
WHERE rp.permission_id = p.id AND p.resource = 'ups';

DELETE FROM permissions WHERE resource = 'ups';
