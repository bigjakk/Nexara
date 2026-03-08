-- Store the reachable IP/address for each node (from corosync/cluster status).
ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS address TEXT NOT NULL DEFAULT '';

-- Index for quick address lookups.
CREATE INDEX IF NOT EXISTS idx_nodes_cluster_address ON nodes (cluster_id, name) WHERE address != '';
