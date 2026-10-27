-- Automatic in-place upgrade — no manual steps, no operator action required.
-- A role assignment scoped to a cluster now exists only while that cluster does.
--
-- user_roles.scope_id names the cluster of a cluster-scoped assignment and had no
-- foreign key, so removing a cluster left every assignment scoped to it behind, for
-- good. Such an assignment granted nothing a removed cluster could still be asked for.
-- But 000122 keeps a removed cluster's audit entries under its old id, and an
-- assignment on that id would grant them — to someone the removal was meant to cut off.
-- (The audit readers also require the cluster to exist; this removes the assignment.)
--
-- What this deletes: each assignment with scope_type 'cluster' whose scope_id names no
-- cluster. It is safe: the cluster is gone, so there is nothing for it to act on, and an
-- administrator's access is a global assignment, which this does not touch. What changes
-- for an admin is that a user's role list no longer shows an assignment on a cluster
-- that was removed.
--
-- Then it adds the foreign key that keeps it so, ON DELETE CASCADE: removing a cluster
-- removes the assignments scoped to it in the same statement, and an assignment naming
-- a cluster that does not exist is refused by the database. The key is validated: after
-- the delete every remaining scope_id names a cluster, and a global assignment carries
-- none (the handler never writes one). The table is a handful of rows.
--
-- Down: drops the key. The deleted assignments are not restored; they granted nothing.

DELETE FROM user_roles ur
WHERE ur.scope_type = 'cluster'
  AND NOT EXISTS (SELECT 1 FROM clusters c WHERE c.id = ur.scope_id);

ALTER TABLE user_roles DROP CONSTRAINT IF EXISTS user_roles_scope_id_fkey;
ALTER TABLE user_roles
    ADD CONSTRAINT user_roles_scope_id_fkey
    FOREIGN KEY (scope_id) REFERENCES clusters(id) ON DELETE CASCADE;
