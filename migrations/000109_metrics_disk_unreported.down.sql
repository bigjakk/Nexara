-- 000109_metrics_disk_unreported.down.sql
--
-- Drops the unreported-disk flag. The counters keep every reading, carried
-- ones included; what goes is only the record of which samples were carried,
-- so a rate measured between samples reads a carried stretch as idle.
ALTER TABLE node_metrics DROP COLUMN IF EXISTS disk_unreported;
ALTER TABLE vm_metrics DROP COLUMN IF EXISTS disk_unreported;
