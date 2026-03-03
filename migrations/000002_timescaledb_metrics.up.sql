-- 000002_timescaledb_metrics.up.sql
-- TimescaleDB hypertables for time-series metrics

CREATE EXTENSION IF NOT EXISTS timescaledb;

-- node_metrics
CREATE TABLE IF NOT EXISTS node_metrics (
    time       TIMESTAMPTZ NOT NULL,
    node_id    UUID NOT NULL,
    cpu_usage  DOUBLE PRECISION NOT NULL DEFAULT 0,
    mem_used   BIGINT NOT NULL DEFAULT 0,
    mem_total  BIGINT NOT NULL DEFAULT 0,
    disk_read  BIGINT NOT NULL DEFAULT 0,
    disk_write BIGINT NOT NULL DEFAULT 0,
    net_in     BIGINT NOT NULL DEFAULT 0,
    net_out    BIGINT NOT NULL DEFAULT 0
);

SELECT create_hypertable('node_metrics', by_range('time'));
CREATE INDEX IF NOT EXISTS idx_node_metrics_node_id_time ON node_metrics (node_id, time DESC);

-- vm_metrics
CREATE TABLE IF NOT EXISTS vm_metrics (
    time       TIMESTAMPTZ NOT NULL,
    vm_id      UUID NOT NULL,
    cpu_usage  DOUBLE PRECISION NOT NULL DEFAULT 0,
    mem_used   BIGINT NOT NULL DEFAULT 0,
    mem_total  BIGINT NOT NULL DEFAULT 0,
    disk_read  BIGINT NOT NULL DEFAULT 0,
    disk_write BIGINT NOT NULL DEFAULT 0,
    net_in     BIGINT NOT NULL DEFAULT 0,
    net_out    BIGINT NOT NULL DEFAULT 0
);

SELECT create_hypertable('vm_metrics', by_range('time'));
CREATE INDEX IF NOT EXISTS idx_vm_metrics_vm_id_time ON vm_metrics (vm_id, time DESC);

-- Continuous aggregate: node_metrics 5-minute rollup
CREATE MATERIALIZED VIEW node_metrics_5m
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('5 minutes', time) AS bucket,
    node_id,
    avg(cpu_usage)   AS avg_cpu_usage,
    max(cpu_usage)   AS max_cpu_usage,
    avg(mem_used)    AS avg_mem_used,
    max(mem_used)    AS max_mem_used,
    avg(mem_total)   AS avg_mem_total,
    avg(disk_read)   AS avg_disk_read,
    avg(disk_write)  AS avg_disk_write,
    avg(net_in)      AS avg_net_in,
    avg(net_out)     AS avg_net_out
FROM node_metrics
GROUP BY bucket, node_id
WITH NO DATA;

-- Continuous aggregate: node_metrics 1-hour rollup
CREATE MATERIALIZED VIEW node_metrics_1h
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time) AS bucket,
    node_id,
    avg(cpu_usage)   AS avg_cpu_usage,
    max(cpu_usage)   AS max_cpu_usage,
    avg(mem_used)    AS avg_mem_used,
    max(mem_used)    AS max_mem_used,
    avg(mem_total)   AS avg_mem_total,
    avg(disk_read)   AS avg_disk_read,
    avg(disk_write)  AS avg_disk_write,
    avg(net_in)      AS avg_net_in,
    avg(net_out)     AS avg_net_out
FROM node_metrics
GROUP BY bucket, node_id
WITH NO DATA;

-- Continuous aggregate: vm_metrics 5-minute rollup
CREATE MATERIALIZED VIEW vm_metrics_5m
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('5 minutes', time) AS bucket,
    vm_id,
    avg(cpu_usage)   AS avg_cpu_usage,
    max(cpu_usage)   AS max_cpu_usage,
    avg(mem_used)    AS avg_mem_used,
    max(mem_used)    AS max_mem_used,
    avg(mem_total)   AS avg_mem_total,
    avg(disk_read)   AS avg_disk_read,
    avg(disk_write)  AS avg_disk_write,
    avg(net_in)      AS avg_net_in,
    avg(net_out)     AS avg_net_out
FROM vm_metrics
GROUP BY bucket, vm_id
WITH NO DATA;

-- Continuous aggregate: vm_metrics 1-hour rollup
CREATE MATERIALIZED VIEW vm_metrics_1h
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time) AS bucket,
    vm_id,
    avg(cpu_usage)   AS avg_cpu_usage,
    max(cpu_usage)   AS max_cpu_usage,
    avg(mem_used)    AS avg_mem_used,
    max(mem_used)    AS max_mem_used,
    avg(mem_total)   AS avg_mem_total,
    avg(disk_read)   AS avg_disk_read,
    avg(disk_write)  AS avg_disk_write,
    avg(net_in)      AS avg_net_in,
    avg(net_out)     AS avg_net_out
FROM vm_metrics
GROUP BY bucket, vm_id
WITH NO DATA;

-- Refresh policies for continuous aggregates
SELECT add_continuous_aggregate_policy('node_metrics_5m',
    start_offset    => INTERVAL '30 minutes',
    end_offset      => INTERVAL '1 minute',
    schedule_interval => INTERVAL '5 minutes');

SELECT add_continuous_aggregate_policy('node_metrics_1h',
    start_offset    => INTERVAL '3 hours',
    end_offset      => INTERVAL '1 hour',
    schedule_interval => INTERVAL '1 hour');

SELECT add_continuous_aggregate_policy('vm_metrics_5m',
    start_offset    => INTERVAL '30 minutes',
    end_offset      => INTERVAL '1 minute',
    schedule_interval => INTERVAL '5 minutes');

SELECT add_continuous_aggregate_policy('vm_metrics_1h',
    start_offset    => INTERVAL '3 hours',
    end_offset      => INTERVAL '1 hour',
    schedule_interval => INTERVAL '1 hour');

-- Retention policy: drop raw data older than 30 days
SELECT add_retention_policy('node_metrics', INTERVAL '30 days');
SELECT add_retention_policy('vm_metrics', INTERVAL '30 days');
