-- 000107_vm_metrics_mem_guest.down.sql
--
-- Drops the memory-source flag. mem_used keeps every reading; what goes is
-- only the record of whose view each one was, so a report can no longer tell
-- a guest's own figure from the host's.
ALTER TABLE vm_metrics DROP COLUMN IF EXISTS mem_guest;
