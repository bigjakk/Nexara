-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Additive: four indexes on tables a cluster's and a node's History read newest
-- first, each on the column its arm orders by (queries/history.sql). The tables
-- are kept for good — the archive of pruned tasks, the guests' status changes,
-- the guests' parts of backup jobs — so without these every read sorted all of
-- each, and a cap of 2,000 rows stopped none of that. Plain CREATE INDEX: the
-- tables date from 000115 and 000116, of the same unreleased version, and are
-- small. One transaction: an interrupted run leaves none of them.
CREATE INDEX IF NOT EXISTS idx_task_history_archive_cluster_started
    ON task_history_archive (cluster_id, started_at);
CREATE INDEX IF NOT EXISTS idx_task_history_archive_cluster_node_started
    ON task_history_archive (cluster_id, node, started_at);
CREATE INDEX IF NOT EXISTS idx_guest_backups_cluster_started
    ON guest_backups (cluster_id, started_at);
CREATE INDEX IF NOT EXISTS idx_guest_status_changes_cluster_observed
    ON guest_status_changes (cluster_id, observed_at);
