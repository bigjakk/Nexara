DROP INDEX IF EXISTS idx_task_history_realm_syncs;
ALTER TABLE cluster_object_changes
    DROP COLUMN IF EXISTS members_left,
    DROP COLUMN IF EXISTS members_joined;
ALTER TABLE cluster_object_baselines DROP COLUMN IF EXISTS signature;
DROP TABLE IF EXISTS cluster_object_presence;
DROP TABLE IF EXISTS cluster_object_state;
