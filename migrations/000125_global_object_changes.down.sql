-- The global rows cannot go back into a column that names a cluster: they are
-- deleted, which is the history of PBS jobs edited on the PBS itself.
DELETE FROM cluster_object_changes WHERE cluster_id IS NULL;
ALTER TABLE cluster_object_changes DROP CONSTRAINT IF EXISTS cluster_object_changes_global_kinds;
ALTER TABLE cluster_object_changes ALTER COLUMN cluster_id SET NOT NULL;
