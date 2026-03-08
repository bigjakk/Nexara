DROP INDEX IF EXISTS idx_nodes_cluster_address;
ALTER TABLE nodes DROP COLUMN IF EXISTS address;
