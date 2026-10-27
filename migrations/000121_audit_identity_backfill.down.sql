-- Restores the identity of every row the up migration re-filed, from the originals
-- it recorded in details.rekeyed_from, and takes that trace out. Only rows that
-- carry this migration's own tag (rekeyed_from.by = 'audit_identity_backfill'): a
-- row another migration re-filed first keeps that migration's trace, and is its
-- down's to restore. The details keys the up added (storage, datastore,
-- resource_name, job_id) stay; the release before this one reads none of them and
-- none is a row's identity. Rows written after the up migration carry no trace
-- and keep the identity they were written with, so after a rollback an object's
-- History can read both spellings until the previous release runs everywhere.
UPDATE audit_log
SET resource_type = details->'rekeyed_from'->>'resource_type',
    resource_id = details->'rekeyed_from'->>'resource_id',
    action = details->'rekeyed_from'->>'action',
    details = details - 'rekeyed_from'
WHERE details->'rekeyed_from'->>'by' = 'audit_identity_backfill';
