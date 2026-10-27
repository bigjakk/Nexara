-- Puts the foreign key back as ON DELETE CASCADE, NOT VALID, drops the trigger
-- and removed_clusters. A foreign key cannot hold over an entry whose cluster
-- has been removed, so those entries are detached first: cluster_id becomes
-- NULL, which keeps the entry (a system-wide one, readable only with global
-- view:audit, the shape the release before this one reads) where deleting it,
-- as the cascade would have, would lose it. The entry then no longer says which
-- cluster it was recorded against, since removed_clusters goes with this
-- migration; nothing else about it changes.

DROP TRIGGER IF EXISTS trg_clusters_record_removed ON clusters;
DROP FUNCTION IF EXISTS record_removed_cluster();

UPDATE audit_log
SET cluster_id = NULL
WHERE cluster_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM clusters c WHERE c.id = audit_log.cluster_id);

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_cluster_id_fkey;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_cluster_id_fkey
    FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE NOT VALID;

DROP TABLE IF EXISTS removed_clusters;
