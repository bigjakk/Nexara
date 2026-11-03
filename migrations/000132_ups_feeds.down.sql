ALTER TABLE nut_servers DROP COLUMN IF EXISTS host_node_name;
ALTER TABLE nut_servers DROP COLUMN IF EXISTS host_cluster_id;
DROP TABLE IF EXISTS ups_feeds;
DROP TABLE IF EXISTS ups_clients;
