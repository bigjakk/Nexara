-- 000114_node_pressure.down.sql
--
-- Drops the nodes' pressure history. TimescaleDB drops the table's retention
-- policy with it. The node RRD still holds the last day.
DROP TABLE IF EXISTS node_pressure;
