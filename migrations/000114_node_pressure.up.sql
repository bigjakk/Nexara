-- 000114_node_pressure.up.sql
--
-- A node's pressure stall information (PSI) over time: the share of each
-- minute in which the node's tasks waited on CPU, IO or memory. Proxmox VE 9
-- records it in the node's RRD, at one-minute steps kept for a day, and serves
-- it through /nodes/{node}/rrddata only — the node status the collector polls
-- does not carry it — so a background pass copies the steps it has not stored
-- yet, and the table keeps them as long as the raw metrics.
--
--   cpu_some             percent of the minute at least one task waited for CPU
--   io_some / io_full    ... at least one / every non-idle task waited for IO
--   mem_some / mem_full  ... at least one / every non-idle task waited for memory
--   NULL                 the RRD had no value for that step
--
-- The kernel's CPU "full" is not in the node RRD, so it is not stored.
-- (node_id, time) is unique: a pass that rereads a step inserts nothing.
--
-- Additive and self-contained: a new table, empty until the first pass. No
-- operator action.
CREATE TABLE IF NOT EXISTS node_pressure (
    time     TIMESTAMPTZ NOT NULL,
    node_id  UUID NOT NULL,
    cpu_some REAL,
    io_some  REAL,
    io_full  REAL,
    mem_some REAL,
    mem_full REAL
);

SELECT create_hypertable('node_pressure', by_range('time'), if_not_exists => TRUE);
CREATE UNIQUE INDEX IF NOT EXISTS idx_node_pressure_node_id_time ON node_pressure (node_id, time);
SELECT add_retention_policy('node_pressure', INTERVAL '30 days', if_not_exists => TRUE);
