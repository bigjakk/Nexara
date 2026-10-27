-- Restores ON DELETE CASCADE and drops the trigger that stamps a removed
-- cluster's entries. Entries already detached from a removed cluster keep their
-- NULL cluster_id and their details.detached_cluster (a NULL violates no foreign
-- key); only what happens to an entry when a cluster is removed from now on
-- changes back.

DROP TRIGGER IF EXISTS trg_clusters_stamp_audit_detached ON clusters;
DROP FUNCTION IF EXISTS stamp_audit_detached_cluster();

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_cluster_id_fkey;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_cluster_id_fkey
    FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE;
