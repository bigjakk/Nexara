-- 000047_leader_election.up.sql
-- Heartbeat-based leader election for the collector and scheduler.
-- See migrations/000047_leader_election.up.sql for rationale.

CREATE TABLE IF NOT EXISTS leader_election (
    role           TEXT PRIMARY KEY,
    holder_id      UUID NOT NULL,
    last_heartbeat TIMESTAMPTZ NOT NULL DEFAULT now()
);
