-- Drops the key that ties a cluster-scoped role assignment to its cluster and the check
-- that only a cluster-scoped assignment carries a scope_id. The assignments the up
-- deleted are not restored: they named a cluster that no longer existed (or repeated a
-- grant) and granted nothing more. A stray id it cleared from a global assignment is not
-- put back.

ALTER TABLE user_roles DROP CONSTRAINT IF EXISTS user_roles_scope_id_fkey;
ALTER TABLE user_roles DROP CONSTRAINT IF EXISTS user_roles_scope_check;
