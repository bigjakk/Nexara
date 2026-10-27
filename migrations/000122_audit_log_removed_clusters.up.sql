-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Keeps an audit entry when the cluster it names is removed, and still says
-- which cluster that was.
--
-- audit_log.cluster_id was ON DELETE CASCADE, so removing a cluster deleted
-- every entry ever recorded against it — the opposite of what an audit log is
-- for, and the defect 000051 fixed for users. The constraint is dropped, and an
-- entry keeps the cluster_id it was written with: removing a cluster no longer
-- touches its entries at all, and the id in the record is the "where".
--
-- Not SET NULL, and not a stamp in the entry's details: either rewrites every
-- entry of the cluster in the transaction that removes it, and rewriting an
-- audit_log row costs nine index entries. Measured on a million entries, half
-- of them one cluster's, that took 22 to 29 seconds against about a second for
-- the cascade, with the cluster's collector waiting on the lock throughout.
--
-- The cluster's NAME goes with the cluster, so a BEFORE DELETE trigger on
-- clusters records the id and name in removed_clusters first (one small row per
-- removed cluster; ON CONFLICT DO NOTHING keeps the first name if an id is ever
-- removed twice), and the readers that label an entry by its cluster fall back
-- to it. A cluster added again gets a new id, so nothing collides.
--
-- Who may read such an entry does not change: its cluster_id names no cluster,
-- so no cluster-scoped grant is meant to reach it, and the scoped readers say so
-- themselves (they require the cluster to exist; role assignments scoped to a
-- removed cluster are not deleted with it). Only a holder of GLOBAL view:audit
-- reads it, as with the entries that have no cluster at all.
--
-- Upgrade: every existing cluster_id references a cluster that exists (the
-- dropped constraint guaranteed it), so no row changes and there is nothing to
-- back-fill; only the constraint goes and the table and trigger are added, in
-- this one transaction. Entries already deleted with a cluster are gone and
-- cannot be recovered.
--
-- Down: drops the trigger and removed_clusters, detaches every entry whose
-- cluster is gone (cluster_id becomes NULL: kept, system-wide, as the release
-- before this one reads such an entry) rather than deleting it as the cascade
-- would have, so that a rollback loses no entry (it no longer says which cluster
-- it was recorded against), and puts the foreign key back as ON DELETE CASCADE,
-- NOT VALID.

CREATE TABLE IF NOT EXISTS removed_clusters (
    id         UUID PRIMARY KEY,
    name       TEXT NOT NULL,
    removed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION record_removed_cluster() RETURNS trigger AS $$
BEGIN
    INSERT INTO removed_clusters (id, name) VALUES (OLD.id, OLD.name) ON CONFLICT DO NOTHING;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_clusters_record_removed ON clusters;
CREATE TRIGGER trg_clusters_record_removed
    BEFORE DELETE ON clusters
    FOR EACH ROW EXECUTE FUNCTION record_removed_cluster();

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_cluster_id_fkey;
