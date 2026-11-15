-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Unhooks three tables from a node row's deletion. Deletes nothing; the one
-- backfill derives a value from rows already there.
--
-- From this release the collector deletes a node row once the cluster stops
-- listing the node as a member (Syncer.pruneRemovedNodes). Three tables
-- referenced nodes(id) ON DELETE CASCADE, so that delete would also have
-- removed:
--   * cve_scan_nodes, and through it cve_scan_vulns: the node's CVE scan
--     history. The rows carry node_name, so they still read without the node.
--   * alert_rules scoped to the node, and through alert_history.rule_id
--     (CASCADE) every alert those rules raised: alert history.
--   * maintenance_windows pinned to the node, past ones included.
-- Each foreign key is dropped, so those rows stay with a node_id that no longer
-- resolves. A dangling id is inert where it is read: the alert engine finds no
-- metrics for the node, and isInMaintenanceWindow matches a window's node_id
-- only against a node being evaluated. (ON DELETE SET NULL was not used: a
-- maintenance window with a NULL node_id covers the whole cluster.)
--
-- Cluster deletion still removes all three: maintenance_windows and alert_rules
-- by their own cluster_id (CASCADE), cve_scan_nodes through cve_scans. A
-- node-scoped alert rule from before rules adopted their node's cluster may
-- have a NULL cluster_id and reached the cluster's deletion only through the
-- node, so step 1 fills its cluster_id from the node first.
--
-- Interrupted: the whole file is one transaction; it rolls back and re-runs
-- cleanly.

-- 1. Node-scoped rules without a cluster take their node's.
UPDATE alert_rules r
SET cluster_id = n.cluster_id
FROM nodes n
WHERE r.node_id = n.id AND r.cluster_id IS NULL;

-- 2. A node row's deletion no longer reaches these tables.
ALTER TABLE cve_scan_nodes DROP CONSTRAINT IF EXISTS cve_scan_nodes_node_id_fkey;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_node_id_fkey;
ALTER TABLE maintenance_windows DROP CONSTRAINT IF EXISTS maintenance_windows_node_id_fkey;
