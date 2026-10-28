-- Automatic in-place upgrade — no manual steps, no operator action required.
-- cluster_object_changes.cluster_id (000119) may now be NULL, for the one kind
-- that belongs to no cluster: a PBS server, whose sync and verify jobs are
-- recorded by 000126. Every existing row has a cluster, so the CHECK below
-- validates without rewriting anything, and a cluster's own rows still go with
-- it (the foreign key keeps ON DELETE CASCADE). A PBS row carries no cluster
-- even when its server is attached to one, so removing that cluster from Nexara
-- does not take the server's history with it.
--
-- The CHECK is the database's half of the rule that only a global kind may name
-- no cluster; the writers are the other (internal/collector/observer.go never
-- writes NULL, and 000126's trigger writes it only for pbs_server). A History
-- reads a global kind's rows by resource_type and resource_id alone.
ALTER TABLE cluster_object_changes ALTER COLUMN cluster_id DROP NOT NULL;

ALTER TABLE cluster_object_changes DROP CONSTRAINT IF EXISTS cluster_object_changes_global_kinds;
ALTER TABLE cluster_object_changes ADD CONSTRAINT cluster_object_changes_global_kinds
    CHECK (cluster_id IS NOT NULL OR resource_type = 'pbs_server');
