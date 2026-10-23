-- 000113_vm_metrics_pressure.down.sql
--
-- Drops the guests' pressure readings. Every other metric is kept; what goes
-- is the record of how long each guest's tasks waited on CPU, IO and memory.
ALTER TABLE vm_metrics DROP COLUMN IF EXISTS pressure_cpu_some;
ALTER TABLE vm_metrics DROP COLUMN IF EXISTS pressure_cpu_full;
ALTER TABLE vm_metrics DROP COLUMN IF EXISTS pressure_io_some;
ALTER TABLE vm_metrics DROP COLUMN IF EXISTS pressure_io_full;
ALTER TABLE vm_metrics DROP COLUMN IF EXISTS pressure_mem_some;
ALTER TABLE vm_metrics DROP COLUMN IF EXISTS pressure_mem_full;
