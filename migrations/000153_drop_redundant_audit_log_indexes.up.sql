-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Drops two audit_log indexes that a wider one already serves. No row changes.
--
-- idx_audit_log_cluster_id (cluster_id, from 000005) is a prefix of
-- idx_audit_log_cluster_created (cluster_id, created_at), and
-- idx_audit_log_cluster_vmid (cluster_id, vmid) WHERE vmid IS NOT NULL (000084)
-- is a prefix of idx_audit_log_cluster_vmid_created (cluster_id, vmid,
-- created_at) under the same predicate. Every read that used the narrow one
-- (the audit list and count by cluster and by guest, the per-guest joins)
-- plans onto the wide one once it is gone, and each audit row then writes two
-- index entries fewer.
--
-- The wide ones are built after startup by db.EnsureHistoryIndexes, not by a
-- migration (see 000117's header), so an install upgrading straight from a
-- release before that has not got them yet. Each narrow index is therefore
-- dropped only when its wide one exists and is valid; otherwise it stays, and
-- costs only its upkeep. Interrupted, the transaction rolls back and the next
-- start runs it again.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
               WHERE c.relname = 'idx_audit_log_cluster_created' AND i.indisvalid) THEN
        DROP INDEX IF EXISTS idx_audit_log_cluster_id;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
               WHERE c.relname = 'idx_audit_log_cluster_vmid_created' AND i.indisvalid) THEN
        DROP INDEX IF EXISTS idx_audit_log_cluster_vmid;
    END IF;
END
$$;
