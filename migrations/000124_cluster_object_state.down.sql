DROP INDEX IF EXISTS idx_task_history_realm_syncs;
-- An earlier form of this migration made audit_row_names; this one makes none,
-- and its down takes the function from a database that ran that form.
DROP FUNCTION IF EXISTS audit_row_names(JSONB);
DROP INDEX IF EXISTS idx_cluster_object_changes_explained_unnamed;
ALTER TABLE cluster_object_changes
    DROP COLUMN IF EXISTS explained_unnamed,
    DROP COLUMN IF EXISTS members_left,
    DROP COLUMN IF EXISTS members_joined;
ALTER TABLE cluster_object_baselines DROP COLUMN IF EXISTS signature;
DROP TABLE IF EXISTS cluster_object_presence;
DROP TABLE IF EXISTS cluster_object_state;
