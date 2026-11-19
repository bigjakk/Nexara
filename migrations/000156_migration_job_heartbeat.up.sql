-- A running migration job's heartbeat (see migration.(*Orchestrator).ReconcileLapsed): stamped when a
-- job is claimed and every half minute while the Nexara instance that runs it follows it. A job still
-- "migrating" whose heartbeat stopped has no instance following it any more, and the sweep ends it.
--
-- Nullable and added empty, which PostgreSQL does without rewriting the table: a job claimed before
-- this release has no heartbeat, and the sweep judges it by updated_at with a far longer bound.
ALTER TABLE migration_jobs ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ;
