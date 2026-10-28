-- Automatic in-place upgrade — no manual steps, no operator action required.
-- A role assignment scoped to a cluster now exists only while that cluster does, and
-- only a cluster-scoped assignment carries a scope_id.
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
-- What it changes in a global assignment: nothing is expected to. A global assignment
-- carries no scope_id (the handler never writes one, and the seeds do not). One written
-- by hand that did would be a wrong fit for the key below — reached by the cascade if
-- its id named a live cluster, a failed validation, and so a dirty upgrade, if it named
-- a missing one. So each such stray id is cleared and the assignment kept. Where that
-- would leave two assignments of one user and one role, which are one grant, the one
-- without a stray id is kept, and else the oldest, and the others are deleted so that
-- uq_user_role_scope still holds.
--
-- Then it adds the two constraints that keep it so. A CHECK states the shape: a scope_id
-- exactly when the scope is a cluster, so a global grant can never carry one. The foreign
-- key, ON DELETE CASCADE, ties the rest to their cluster: removing a cluster removes the
-- assignments scoped to it in the same statement, and an assignment naming a cluster that
-- does not exist is refused by the database. Both are validated against rows the
-- statements above have already made fit, so neither can fail; the table is a handful of
-- rows.
--
-- Down: drops both constraints. The deleted assignments are not restored (they granted
-- nothing), and a cleared stray id is not put back.

DELETE FROM user_roles ur
WHERE ur.scope_type = 'cluster'
  AND NOT EXISTS (SELECT 1 FROM clusters c WHERE c.id = ur.scope_id);

DELETE FROM user_roles ur
USING (
    SELECT id,
           row_number() OVER (PARTITION BY user_id, role_id
                              ORDER BY (scope_id IS NULL) DESC, created_at, id) AS rn
    FROM user_roles
    WHERE scope_type <> 'cluster'
) ranked
WHERE ur.id = ranked.id AND ranked.rn > 1;

UPDATE user_roles SET scope_id = NULL WHERE scope_type <> 'cluster' AND scope_id IS NOT NULL;

ALTER TABLE user_roles DROP CONSTRAINT IF EXISTS user_roles_scope_check;
ALTER TABLE user_roles
    ADD CONSTRAINT user_roles_scope_check
    CHECK ((scope_type = 'cluster') = (scope_id IS NOT NULL));

ALTER TABLE user_roles DROP CONSTRAINT IF EXISTS user_roles_scope_id_fkey;
ALTER TABLE user_roles
    ADD CONSTRAINT user_roles_scope_id_fkey
    FOREIGN KEY (scope_id) REFERENCES clusters(id) ON DELETE CASCADE;
