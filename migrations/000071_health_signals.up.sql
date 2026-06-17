-- 000071_health_signals.up.sql
-- Additional infrastructure-health signals surfaced in the health indicator:
-- cluster quorum, node root-filesystem usage, guest lock state (io-error), and
-- storage-replication job status.
--
-- All additive (new nullable/defaulted columns + a new table) — safe for
-- in-place upgrade. Existing rows get the defaults; the collector backfills on
-- the next sync. quorate defaults to true so a cluster that hasn't reported yet
-- is not falsely flagged as having lost quorum.

ALTER TABLE clusters ADD COLUMN IF NOT EXISTS quorate BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE nodes    ADD COLUMN IF NOT EXISTS rootfs_used BIGINT NOT NULL DEFAULT 0;
ALTER TABLE vms      ADD COLUMN IF NOT EXISTS lock_state TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS replication_jobs (
    cluster_id   UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    job_id       TEXT NOT NULL,
    guest        INT NOT NULL DEFAULT 0,
    node         TEXT NOT NULL DEFAULT '',
    target       TEXT NOT NULL DEFAULT '',
    fail_count   INT NOT NULL DEFAULT 0,
    error        TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, job_id)
);
