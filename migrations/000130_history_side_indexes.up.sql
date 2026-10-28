-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Additive: five indexes on tables a cluster's and a node's History read newest
-- first, each on the column its arm orders by (queries/history.sql), the last
-- the changes seen in Proxmox of a node's interfaces, which its History reads by
-- the node they name among those nothing explained. The tables are kept for
-- good — the archive of pruned tasks, the guests' status changes, the guests'
-- parts of backup jobs, the changes seen — so without these every read sorted
-- all of each, and a cap of 2,000 rows stopped none of that: an all-time read of
-- a node's interfaces scanned every interface change of its cluster. Plain
-- CREATE INDEX: the tables date from 000115, 000116 and 000119, of the same
-- unreleased version, and are small. One transaction: an interrupted run leaves
-- none of them.
CREATE INDEX IF NOT EXISTS idx_task_history_archive_cluster_started
    ON task_history_archive (cluster_id, started_at);
CREATE INDEX IF NOT EXISTS idx_task_history_archive_cluster_node_started
    ON task_history_archive (cluster_id, node, started_at);
CREATE INDEX IF NOT EXISTS idx_guest_backups_cluster_started
    ON guest_backups (cluster_id, started_at);
CREATE INDEX IF NOT EXISTS idx_guest_status_changes_cluster_observed
    ON guest_status_changes (cluster_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_cluster_object_changes_node
    ON cluster_object_changes (cluster_id, resource_type, node, observed_at)
    WHERE explained = false AND vmid IS NULL;
