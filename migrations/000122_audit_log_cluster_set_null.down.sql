-- Restores ON DELETE CASCADE. Entries already detached from a removed cluster
-- keep their NULL cluster_id (a NULL violates no foreign key); only what happens
-- to an entry when a cluster is removed from now on changes back.

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_cluster_id_fkey;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_cluster_id_fkey
    FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE;
