-- 000113_vm_metrics_pressure.up.sql
--
-- Records each running guest's pressure stall information (PSI): the share of
-- the last ten seconds in which its tasks waited on CPU, IO or memory, as the
-- kernel accounts it for the guest's cgroup. "some" is time at least one task
-- waited; "full" is time every non-idle task waited at once. A storage stall
-- that never shows as low throughput — the guest simply waits — shows here.
-- Proxmox VE 9 reports these in the guest lists the collector already reads
-- every cycle.
--
--   0–100  percent of the last ten seconds, as PVE reported it
--   NULL   no reading — the guest was not running, PVE did not report it
--          (Proxmox VE 8), or the row predates these columns
--
-- Additive and self-contained. Nullable columns with no default are a catalog
-- change only: existing chunks are not rewritten, existing rows read NULL, and
-- the continuous aggregates (000002) select their columns by name, so they are
-- untouched. No operator action.
ALTER TABLE vm_metrics ADD COLUMN IF NOT EXISTS pressure_cpu_some REAL;
ALTER TABLE vm_metrics ADD COLUMN IF NOT EXISTS pressure_cpu_full REAL;
ALTER TABLE vm_metrics ADD COLUMN IF NOT EXISTS pressure_io_some REAL;
ALTER TABLE vm_metrics ADD COLUMN IF NOT EXISTS pressure_io_full REAL;
ALTER TABLE vm_metrics ADD COLUMN IF NOT EXISTS pressure_mem_some REAL;
ALTER TABLE vm_metrics ADD COLUMN IF NOT EXISTS pressure_mem_full REAL;

COMMENT ON COLUMN vm_metrics.pressure_cpu_some IS 'Percent of the last 10 s in which at least one of the guest''s tasks waited for CPU (PSI some avg10); NULL when there was no reading';
COMMENT ON COLUMN vm_metrics.pressure_cpu_full IS 'Percent of the last 10 s in which all of the guest''s non-idle tasks waited for CPU (PSI full avg10); NULL when there was no reading';
COMMENT ON COLUMN vm_metrics.pressure_io_some IS 'Percent of the last 10 s in which at least one of the guest''s tasks waited for IO (PSI some avg10); NULL when there was no reading';
COMMENT ON COLUMN vm_metrics.pressure_io_full IS 'Percent of the last 10 s in which all of the guest''s non-idle tasks waited for IO (PSI full avg10); NULL when there was no reading';
COMMENT ON COLUMN vm_metrics.pressure_mem_some IS 'Percent of the last 10 s in which at least one of the guest''s tasks waited for memory (PSI some avg10); NULL when there was no reading';
COMMENT ON COLUMN vm_metrics.pressure_mem_full IS 'Percent of the last 10 s in which all of the guest''s non-idle tasks waited for memory (PSI full avg10); NULL when there was no reading';
