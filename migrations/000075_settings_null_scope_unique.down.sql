-- Revert the settings uniqueness constraint to the NULL-distinct form from
-- 000001. Dropping NULLS NOT DISTINCT only ever loosens the constraint, so no
-- existing row can violate the restored version and no data check is needed.
--
-- This does NOT restore the duplicate rows the up migration deleted — they were
-- superseded values and are unrecoverable outside a database backup. It also
-- reinstates the underlying defect: with NULLs distinct again, the
-- `ON CONFLICT (key, scope, scope_id)` inference in queries/settings.sql stops
-- matching shared-scope rows and every such write appends instead of updating.

ALTER TABLE settings DROP CONSTRAINT IF EXISTS settings_key_scope_scope_id_key;
ALTER TABLE settings ADD  CONSTRAINT settings_key_scope_scope_id_key
    UNIQUE (key, scope, scope_id);
