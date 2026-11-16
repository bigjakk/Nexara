-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Purely additive: five columns, each NOT NULL with a DEFAULT that PostgreSQL
-- fills on existing rows, and two partial indexes. No row is changed or deleted.
--
-- clusters.corosync_name is the corosync cluster name the cluster row's nodes
-- were read from (GET /cluster/status, its "cluster" entry), recorded by the
-- collector the first time a pass can tell which nodes the cluster holds. It
-- replaces a copy the collector kept in memory, which a restart lost: a row
-- whose API URL was moved to another cluster while Nexara was down adopted the
-- other cluster's name on the first pass and removed its own nodes. Empty
-- until that first pass, and for a single-node install, which has no corosync
-- name and keeps the earlier behaviour (no node row is ever removed for it).
--
-- clusters.corosync_name_reported is the other name a reading reported while
-- it differs from corosync_name ('' when they agree). While it is set the
-- collector removes no node row from the cluster and the clusters API reports
-- a "cluster_name_changed" issue. It clears itself when the cluster reports
-- the recorded name again; POST /api/v1/clusters/:id/accept-cluster-name makes
-- the reported name the recorded one, for a deliberate rename or move.
--
-- alert_rules.detached_node_name / detached_enabled and
-- maintenance_windows.detached_node_name keep the name of the node a removed
-- node row had (and whether the rule was on), so the rows reattach to the new
-- node row when a node of that name rejoins the cluster (a new id). '' for
-- every row today: a rule whose node was removed by an earlier release has no
-- node row left to read a name from and stays as it is.
--
-- Interrupted: the whole file is one transaction; it rolls back and re-runs
-- cleanly (every statement is IF NOT EXISTS).

ALTER TABLE clusters ADD COLUMN IF NOT EXISTS corosync_name TEXT NOT NULL DEFAULT '';
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS corosync_name_reported TEXT NOT NULL DEFAULT '';
COMMENT ON COLUMN clusters.corosync_name IS 'The corosync cluster name this row''s nodes were read from; '''' until recorded or for a single node';
COMMENT ON COLUMN clusters.corosync_name_reported IS 'Another corosync name the API URL answered with; while set, no node row is removed';

ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS detached_node_name TEXT NOT NULL DEFAULT '';
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS detached_enabled BOOLEAN NOT NULL DEFAULT false;
COMMENT ON COLUMN alert_rules.detached_node_name IS 'The name of the removed node this rule waits for; it reattaches when that name rejoins';
COMMENT ON COLUMN alert_rules.detached_enabled IS 'Whether the rule was enabled when its node was removed; it is turned back on when it reattaches';

ALTER TABLE maintenance_windows ADD COLUMN IF NOT EXISTS detached_node_name TEXT NOT NULL DEFAULT '';
COMMENT ON COLUMN maintenance_windows.detached_node_name IS 'The name of the removed node this window waits for; it reattaches when that name rejoins';

CREATE INDEX IF NOT EXISTS idx_alert_rules_detached_node
    ON alert_rules (cluster_id, detached_node_name) WHERE detached_node_name <> '';
CREATE INDEX IF NOT EXISTS idx_maintenance_windows_detached_node
    ON maintenance_windows (cluster_id, detached_node_name) WHERE detached_node_name <> '';
