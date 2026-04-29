-- 000047_leader_election.up.sql
-- Heartbeat-based leader election for the collector and scheduler.
--
-- Replaces the previous pg_try_advisory_lock approach, which left orphan
-- session locks when a replica was hard-killed (TCP keepalive can hold a
-- zombie session for ~2h on default Linux kernels). With a heartbeat row,
-- a stale leader is taken over within `takeover_after_seconds` regardless
-- of what the underlying TCP socket thinks.

CREATE TABLE IF NOT EXISTS leader_election (
    role           TEXT PRIMARY KEY,
    holder_id      UUID NOT NULL,
    last_heartbeat TIMESTAMPTZ NOT NULL DEFAULT now()
);
