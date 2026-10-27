-- Restores the key of every row this migration's up re-filed, from the originals it
-- recorded in the row's details.rekeyed_from (the first original, where two of its
-- statements touched a row), and takes the marks it added back out. Only rows tagged
-- with this migration's name are restored: another migration's rekeyed_from is its
-- own to restore. Rows written after the up migration — by this release's handlers
-- and collector — carry no tag and keep the key they were written under, so after a
-- rollback an object's History can read both spellings until the previous release's
-- code is running on all of it. The rest of the old shape also survives in details
-- (name, path, sid, position, task_type).
UPDATE audit_log
SET resource_type = details->'rekeyed_from'->>'resource_type',
    resource_id = details->'rekeyed_from'->>'resource_id',
    action = details->'rekeyed_from'->>'action',
    details = details - 'rekeyed_from' - 'parent_upid'
WHERE details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys';

-- The row the up leaves when it kept copies of Nexara's applies on the user's word alone.
DELETE FROM audit_log
WHERE resource_type = 'system' AND action = 'migration_notice'
  AND details->>'migration' = 'cluster_object_audit_keys';
