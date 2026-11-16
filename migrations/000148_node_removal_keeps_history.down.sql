-- Puts the three foreign keys back as they were (ON DELETE CASCADE). NOT VALID:
-- rows whose node was removed while they were unhooked keep their dangling
-- node_id instead of failing the down migration or being deleted by it; the
-- constraint holds for every row written from here on. The cluster_id the up
-- migration filled in is left: it is the rule's node's cluster, and is right.

ALTER TABLE cve_scan_nodes DROP CONSTRAINT IF EXISTS cve_scan_nodes_node_id_fkey;
ALTER TABLE cve_scan_nodes ADD CONSTRAINT cve_scan_nodes_node_id_fkey
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE NOT VALID;

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_node_id_fkey;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_node_id_fkey
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE NOT VALID;

ALTER TABLE maintenance_windows DROP CONSTRAINT IF EXISTS maintenance_windows_node_id_fkey;
ALTER TABLE maintenance_windows ADD CONSTRAINT maintenance_windows_node_id_fkey
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE NOT VALID;
