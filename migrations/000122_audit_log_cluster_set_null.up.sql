-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Keeps an audit entry when the cluster it names is removed.
--
-- audit_log.cluster_id was ON DELETE CASCADE, so removing a cluster deleted
-- every entry ever recorded against it — the opposite of what an audit log is
-- for, and the defect 000051 fixed for users. It becomes ON DELETE SET NULL: the
-- entries stay, with their details, and a NULL cluster_id marks a system-wide
-- entry, which only a holder of GLOBAL view:audit reads. A cluster-scoped reader
-- therefore stops seeing a removed cluster's entries, which is the safe side to
-- err on: the cluster that scoped them no longer exists to scope them to.
--
-- The column is already nullable (000014) and every existing value references a
-- cluster that exists, so no row changes and none can violate the new
-- constraint; only the constraint is replaced, in this one transaction. Entries
-- already deleted with a cluster are gone and cannot be recovered.

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_cluster_id_fkey;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_cluster_id_fkey
    FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE SET NULL;
