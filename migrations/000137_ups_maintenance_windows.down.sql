-- 000137_ups_maintenance_windows.down.sql
-- Remove the UPS windows before cluster_id can be NOT NULL again.
DROP INDEX IF EXISTS idx_maintenance_windows_ups;

DELETE FROM maintenance_windows WHERE ups_id IS NOT NULL;

ALTER TABLE maintenance_windows
  DROP CONSTRAINT IF EXISTS maintenance_windows_node_in_cluster_check;

ALTER TABLE maintenance_windows
  DROP CONSTRAINT IF EXISTS maintenance_windows_one_target_check;

ALTER TABLE maintenance_windows DROP COLUMN IF EXISTS ups_id;
ALTER TABLE maintenance_windows ALTER COLUMN cluster_id SET NOT NULL;
