-- Drops the key that ties a cluster-scoped role assignment to its cluster. The
-- assignments the up deleted are not restored: they named a cluster that no longer
-- existed and granted nothing.

ALTER TABLE user_roles DROP CONSTRAINT IF EXISTS user_roles_scope_id_fkey;
