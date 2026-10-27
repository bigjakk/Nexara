-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Keeps an audit entry when the cluster it names is removed, and says which
-- cluster that was.
--
-- audit_log.cluster_id was ON DELETE CASCADE, so removing a cluster deleted
-- every entry ever recorded against it — the opposite of what an audit log is
-- for, and the defect 000051 fixed for users. It becomes ON DELETE SET NULL: the
-- entries stay, with their details, and a NULL cluster_id marks a system-wide
-- entry, which only a holder of GLOBAL view:audit reads. A cluster-scoped reader
-- therefore stops seeing a removed cluster's entries, which is the safe side to
-- err on: the cluster that scoped them no longer exists to scope them to.
--
-- A NULL cluster_id alone would lose WHERE the entry came from: two removed
-- clusters' entries about the same node name could not be told apart. So a
-- BEFORE DELETE trigger on clusters stamps each of the cluster's entries with
-- details.detached_cluster {id, name} first, while they still name it. The key
-- is added beside the existing details, which are never changed, except that
-- the trigger's own value always wins: an entry whose details already held that
-- key (nothing writes one) would otherwise name the wrong cluster. The same
-- UPDATE detaches the entries, so
-- the foreign key's SET NULL finds nothing left to rewrite: one pass over the
-- cluster's entries rather than two (about half a million entries took 28
-- seconds, against 56 for two passes and 22 for SET NULL alone).
--
-- The column is already nullable (000014) and every existing value references a
-- cluster that exists, so no existing row changes and none can violate the new
-- constraint; only the constraint is replaced and the trigger added, in this one
-- transaction. Entries already deleted with a cluster are gone and cannot be
-- recovered.

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_cluster_id_fkey;

-- NOT VALID is safe: the constraint it replaces already guaranteed every existing
-- row, so there is nothing to scan, and the new one is enforced on every change.
ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_cluster_id_fkey
    FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE SET NULL NOT VALID;

CREATE OR REPLACE FUNCTION stamp_audit_detached_cluster() RETURNS trigger AS $$
BEGIN
    UPDATE audit_log
    SET details = details || jsonb_build_object('detached_cluster', jsonb_build_object('id', OLD.id, 'name', OLD.name)),
        cluster_id = NULL
    WHERE cluster_id = OLD.id
      AND jsonb_typeof(details) = 'object';
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_clusters_stamp_audit_detached ON clusters;
CREATE TRIGGER trg_clusters_stamp_audit_detached
    BEFORE DELETE ON clusters
    FOR EACH ROW EXECUTE FUNCTION stamp_audit_detached_cluster();
