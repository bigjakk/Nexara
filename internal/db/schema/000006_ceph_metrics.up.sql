-- 000006_ceph_metrics.up.sql
-- TimescaleDB hypertables for Ceph storage metrics

-- ceph_cluster_metrics: cluster-wide Ceph health and capacity
CREATE TABLE IF NOT EXISTS ceph_cluster_metrics (
    time            TIMESTAMPTZ NOT NULL,
    cluster_id      UUID NOT NULL,
    health_status   TEXT NOT NULL DEFAULT 'HEALTH_UNKNOWN',
    osds_total      INT NOT NULL DEFAULT 0,
    osds_up         INT NOT NULL DEFAULT 0,
    osds_in         INT NOT NULL DEFAULT 0,
    pgs_total       INT NOT NULL DEFAULT 0,
    bytes_used      BIGINT NOT NULL DEFAULT 0,
    bytes_avail     BIGINT NOT NULL DEFAULT 0,
    bytes_total     BIGINT NOT NULL DEFAULT 0,
    read_ops_sec    BIGINT NOT NULL DEFAULT 0,
    write_ops_sec   BIGINT NOT NULL DEFAULT 0,
    read_bytes_sec  BIGINT NOT NULL DEFAULT 0,
    write_bytes_sec BIGINT NOT NULL DEFAULT 0
);

SELECT create_hypertable('ceph_cluster_metrics', by_range('time'));
CREATE INDEX IF NOT EXISTS idx_ceph_cluster_metrics_cluster_time
    ON ceph_cluster_metrics (cluster_id, time DESC);

-- ceph_osd_metrics: per-OSD metrics
CREATE TABLE IF NOT EXISTS ceph_osd_metrics (
    time        TIMESTAMPTZ NOT NULL,
    cluster_id  UUID NOT NULL,
    osd_id      INT NOT NULL,
    osd_name    TEXT NOT NULL DEFAULT '',
    host        TEXT NOT NULL DEFAULT '',
    status_up   BOOLEAN NOT NULL DEFAULT false,
    status_in   BOOLEAN NOT NULL DEFAULT false,
    crush_weight DOUBLE PRECISION NOT NULL DEFAULT 0
);

SELECT create_hypertable('ceph_osd_metrics', by_range('time'));
CREATE INDEX IF NOT EXISTS idx_ceph_osd_metrics_cluster_osd_time
    ON ceph_osd_metrics (cluster_id, osd_id, time DESC);

-- ceph_pool_metrics: per-pool metrics
CREATE TABLE IF NOT EXISTS ceph_pool_metrics (
    time            TIMESTAMPTZ NOT NULL,
    cluster_id      UUID NOT NULL,
    pool_id         INT NOT NULL,
    pool_name       TEXT NOT NULL DEFAULT '',
    size            INT NOT NULL DEFAULT 0,
    min_size        INT NOT NULL DEFAULT 0,
    pg_num          INT NOT NULL DEFAULT 0,
    bytes_used      BIGINT NOT NULL DEFAULT 0,
    percent_used    DOUBLE PRECISION NOT NULL DEFAULT 0,
    read_ops_sec    BIGINT NOT NULL DEFAULT 0,
    write_ops_sec   BIGINT NOT NULL DEFAULT 0,
    read_bytes_sec  BIGINT NOT NULL DEFAULT 0,
    write_bytes_sec BIGINT NOT NULL DEFAULT 0
);

SELECT create_hypertable('ceph_pool_metrics', by_range('time'));
CREATE INDEX IF NOT EXISTS idx_ceph_pool_metrics_cluster_pool_time
    ON ceph_pool_metrics (cluster_id, pool_id, time DESC);

-- Continuous aggregate: ceph_cluster_metrics 5-minute rollup
CREATE MATERIALIZED VIEW ceph_cluster_metrics_5m
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('5 minutes', time) AS bucket,
    cluster_id,
    last(health_status, time)    AS health_status,
    avg(osds_total)::int         AS avg_osds_total,
    avg(osds_up)::int            AS avg_osds_up,
    avg(osds_in)::int            AS avg_osds_in,
    avg(pgs_total)::int          AS avg_pgs_total,
    avg(bytes_used)              AS avg_bytes_used,
    avg(bytes_avail)             AS avg_bytes_avail,
    avg(bytes_total)             AS avg_bytes_total,
    avg(read_ops_sec)            AS avg_read_ops_sec,
    avg(write_ops_sec)           AS avg_write_ops_sec,
    avg(read_bytes_sec)          AS avg_read_bytes_sec,
    avg(write_bytes_sec)         AS avg_write_bytes_sec
FROM ceph_cluster_metrics
GROUP BY bucket, cluster_id
WITH NO DATA;

-- Continuous aggregate: ceph_cluster_metrics 1-hour rollup
CREATE MATERIALIZED VIEW ceph_cluster_metrics_1h
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time) AS bucket,
    cluster_id,
    last(health_status, time)   AS health_status,
    avg(osds_total)::int        AS avg_osds_total,
    avg(osds_up)::int           AS avg_osds_up,
    avg(osds_in)::int           AS avg_osds_in,
    avg(pgs_total)::int         AS avg_pgs_total,
    avg(bytes_used)             AS avg_bytes_used,
    avg(bytes_avail)            AS avg_bytes_avail,
    avg(bytes_total)            AS avg_bytes_total,
    avg(read_ops_sec)           AS avg_read_ops_sec,
    avg(write_ops_sec)          AS avg_write_ops_sec,
    avg(read_bytes_sec)         AS avg_read_bytes_sec,
    avg(write_bytes_sec)        AS avg_write_bytes_sec
FROM ceph_cluster_metrics
GROUP BY bucket, cluster_id
WITH NO DATA;

-- Refresh policies
SELECT add_continuous_aggregate_policy('ceph_cluster_metrics_5m',
    start_offset    => INTERVAL '30 minutes',
    end_offset      => INTERVAL '1 minute',
    schedule_interval => INTERVAL '5 minutes');

SELECT add_continuous_aggregate_policy('ceph_cluster_metrics_1h',
    start_offset    => INTERVAL '3 hours',
    end_offset      => INTERVAL '1 hour',
    schedule_interval => INTERVAL '1 hour');

-- Retention policies: drop raw data older than 30 days
SELECT add_retention_policy('ceph_cluster_metrics', INTERVAL '30 days');
SELECT add_retention_policy('ceph_osd_metrics', INTERVAL '30 days');
SELECT add_retention_policy('ceph_pool_metrics', INTERVAL '30 days');
