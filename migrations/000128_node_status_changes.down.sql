DROP INDEX IF EXISTS idx_task_history_cluster_node_started;
DROP TRIGGER IF EXISTS trg_nodes_status_change ON nodes;
DROP FUNCTION IF EXISTS record_node_status_change();
DROP TABLE IF EXISTS node_status_changes;
