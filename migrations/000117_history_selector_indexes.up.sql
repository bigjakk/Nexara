-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Purely additive: two indexes on audit_log and one SQL function. Nothing is
-- rewritten or backfilled; an interrupted run leaves nothing behind, since the
-- migration is one transaction, and every statement is IF NOT EXISTS or
-- OR REPLACE, so a re-run is safe.
--
-- The indexes are built inside that transaction, so audit writes wait while
-- they build: on dev's 32k audit rows that is well under a second, on an
-- install with millions of rows it lengthens this one startup by the build.
--
-- They serve the History's generic selector (queries/history.sql,
-- ListHistory), which reads one object's audit rows by its identity:
--
--   idx_audit_log_resource   (resource_type, resource_id): the object's own
--                            rows. The existing (resource_type, created_at)
--                            index reads every row of the type to find them.
--   idx_audit_log_details    GIN (details jsonb_path_ops): the rows that name
--                            the object in a details key — a storage in a
--                            guest's disk move, a node in a network change.
--                            The selector asks details @> {"key": "value"}; a
--                            filter on details->>'key' read every row of the
--                            type, 105 ms on 10x dev's rows against 2-5 ms.
--
-- history_worker_ok is the selector's test of a guest row's Proxmox worker
-- type against a kind's patterns (LIKE, any of them; none = every row), written
-- once for the several arms of ListHistory that need the same answer. A row
-- with no UPID has the worker type ''.
CREATE INDEX IF NOT EXISTS idx_audit_log_resource
    ON audit_log (resource_type, resource_id);

CREATE INDEX IF NOT EXISTS idx_audit_log_details
    ON audit_log USING gin (details jsonb_path_ops);

CREATE OR REPLACE FUNCTION history_worker_ok(upid text, patterns text[]) RETURNS boolean
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
AS $$
    SELECT cardinality($2) = 0 OR COALESCE(split_part($1, ':', 6), '') LIKE ANY($2)
$$;
