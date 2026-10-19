-- 000109_metrics_disk_unreported.up.sql
--
-- Records the samples whose disk counters PVE did not report. Under load,
-- PVE's full status for a running VM can come back without QEMU's blockstats
-- (they arrive over QMP, which then fails to answer in time), reading 0 for
-- diskread and diskwrite while CPU, memory and network are real. A node's
-- counters are the sum of its guests', so they dip with it. Stored as read,
-- the guest's whole lifetime of disk bytes vanished and came back: a spike in
-- the charts, the live rates, the alerts and the per-day rates. The collector
-- now keeps such a counter at its last reported value, for up to six hours,
-- and sets this flag, so a consumer measuring rates between samples can
-- measure across the flagged ones.
--
--   TRUE   PVE did not report some of the row's disk counters this sample: a
--          VM's are its last reported ones; a node's sums include such a
--          guest, or miss guests whose list could not be read
--   FALSE  the disk counters are this sample's reading — or the row predates
--          this column, when an unreported sample was stored as read
--
-- Additive and self-contained. A NOT NULL column with a constant default is a
-- catalog change only: existing chunks are not rewritten, existing rows read
-- FALSE, and the continuous aggregates (000002) select their columns by name,
-- so they are untouched. No operator action.
ALTER TABLE vm_metrics ADD COLUMN IF NOT EXISTS disk_unreported BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE node_metrics ADD COLUMN IF NOT EXISTS disk_unreported BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN vm_metrics.disk_unreported IS 'TRUE when PVE did not report the guest''s disk counters this sample and they are its last reported ones, carried forward; FALSE when they are this sample''s reading, or the row predates the column';
COMMENT ON COLUMN node_metrics.disk_unreported IS 'TRUE when the node''s disk sums include a guest whose disk counters PVE did not report this sample (carried forward), or miss guests whose list could not be read; FALSE when they are this sample''s reading, or the row predates the column';
