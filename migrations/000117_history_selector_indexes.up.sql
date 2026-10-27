-- Automatic in-place upgrade — no manual steps, no operator action required.
-- One SQL function, created OR REPLACE: nothing is read, rewritten or
-- backfilled, and a re-run is safe.
--
-- history_worker_ok is the History selector's test of a guest row's Proxmox
-- worker type against a kind's patterns (LIKE, any of them; none — or NULL —
-- is every row), written once for the several arms of ListHistory
-- (queries/history.sql) that need the same answer. A row with no UPID has the
-- worker type ''.
--
-- The two audit_log indexes the selector reads are NOT built here, although
-- the file keeps its name: they are built after startup, once, by
-- db.EnsureHistoryIndexes (internal/db/history_indexes.go), with CREATE INDEX
-- CONCURRENTLY. A migration runs inside the container's healthcheck window, the
-- GIN index over audit_log.details takes minutes on a large install, and a
-- build killed there would leave this migration dirty — every later start
-- would then stop until an operator forced the version. Built concurrently
-- after startup, they block no audit write, and an interrupted build is
-- dropped and redone on the next start. Until they exist the History works,
-- only slower.
CREATE OR REPLACE FUNCTION history_worker_ok(upid text, patterns text[]) RETURNS boolean
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
AS $$
    SELECT cardinality(COALESCE($2, '{}')) = 0 OR COALESCE(split_part($1, ':', 6), '') LIKE ANY($2)
$$;
