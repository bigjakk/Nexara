-- 000137_ups_maintenance_windows.up.sql
-- Maintenance windows on a UPS (Phase 2c): a window names a cluster, as
-- before, or one UPS (maintenance_windows.ups_id). A UPS under a window drops
-- out of the global UPS alert rules, and its own rules hold.
--
-- Purely additive: cluster_id loses its NOT NULL, which no existing row needs;
-- a nullable ups_id; and two CHECKs every existing row meets — each names a
-- cluster and no UPS, and a node only with its cluster. A UPS's windows go
-- with it (CASCADE), as a cluster's do.

ALTER TABLE maintenance_windows ALTER COLUMN cluster_id DROP NOT NULL;
ALTER TABLE maintenance_windows ADD COLUMN IF NOT EXISTS ups_id UUID REFERENCES ups_devices(id) ON DELETE CASCADE;

COMMENT ON COLUMN maintenance_windows.ups_id IS 'The UPS the window covers; NULL for a cluster''s window, which names cluster_id instead';

ALTER TABLE maintenance_windows
  DROP CONSTRAINT IF EXISTS maintenance_windows_one_target_check;

ALTER TABLE maintenance_windows
  ADD CONSTRAINT maintenance_windows_one_target_check
  CHECK ((cluster_id IS NULL) <> (ups_id IS NULL));

ALTER TABLE maintenance_windows
  DROP CONSTRAINT IF EXISTS maintenance_windows_node_in_cluster_check;

ALTER TABLE maintenance_windows
  ADD CONSTRAINT maintenance_windows_node_in_cluster_check
  CHECK (node_id IS NULL OR cluster_id IS NOT NULL);

CREATE INDEX IF NOT EXISTS idx_maintenance_windows_ups ON maintenance_windows (ups_id, starts_at DESC)
  WHERE ups_id IS NOT NULL;
