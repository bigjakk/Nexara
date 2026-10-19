-- 000107_vm_metrics_mem_guest.up.sql
--
-- Records whose view each guest memory reading is. PVE's `mem` for a VM is
-- the guest's own figure (total minus free from the balloon driver's
-- statistics) only when the driver reports them; without them it is the memory
-- the QEMU process holds on the node, which a guest that has touched its RAM
-- holds near or above its allocation forever, page cache and all. The two
-- read the same in mem_used, so a report could not tell a guest under memory
-- pressure from one that merely booted.
--
--   TRUE  the guest's own figure: balloon statistics, or a container's cgroup
--   FALSE the host's view of a VM without balloon statistics
--   NULL  no reading — the guest was not running, or the row predates this column
--
-- Additive and self-contained. A nullable column with no default is a catalog
-- change only: existing chunks are not rewritten, existing rows read NULL, and
-- the continuous aggregates (000002) select their columns by name, so they are
-- untouched. No operator action.
ALTER TABLE vm_metrics ADD COLUMN IF NOT EXISTS mem_guest BOOLEAN;

COMMENT ON COLUMN vm_metrics.mem_guest IS 'TRUE when mem_used is the guest''s own figure (balloon statistics, or a container''s cgroup), FALSE when it is the host''s view of a VM without balloon statistics (memory the QEMU process holds, can exceed mem_total), NULL when there was no reading (guest not running, or the row predates the column)';
