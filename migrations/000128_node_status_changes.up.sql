-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Purely additive: one new table, one trigger function and one trigger on
-- nodes, and an index on task_history. Nothing existing is read, rewritten or
-- backfilled, so an interrupted run leaves nothing behind — the migration is
-- one transaction.
--
-- Both back a node's History tab
-- (GET /api/v1/clusters/:cluster_id/nodes/:node_name/history, queries/history.sql).

-- node_status_changes records each change of a node's status (online, offline,
-- unknown) as the inventory row saw it. The trigger below writes it, so every
-- writer of nodes.status is covered: the slow pass (UpsertNode) and the fast
-- loop (UpdateNodeStatusFast). A node row is never deleted — it is upserted on
-- (cluster_id, name) — so a node re-added under its name continues its
-- History; node is the name, copied, all the same.
--
-- "unknown" means only that pvestatd has not reported: the History leaves out
-- a round trip through it shorter than two minutes, at read time, rather than
-- a classification pass rewriting rows.
CREATE TABLE IF NOT EXISTS node_status_changes (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id   UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    node         TEXT NOT NULL,
    from_status  TEXT NOT NULL,
    to_status    TEXT NOT NULL,
    -- When a slow pass last saw the node, before the change: it happened after
    -- this and by observed_at.
    last_seen_at TIMESTAMPTZ NOT NULL,
    observed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_node_status_changes_node
    ON node_status_changes (cluster_id, node, observed_at);

-- Kept to statements with no constraint a row can violate: a failure here
-- fails the inventory write that caused it. Twenty changes an hour a node at
-- most, as the observer caps its changes: past that, the hour's last row takes
-- the change in — it ends where the node now is — and a change straight back
-- cancels it, so a flapping node writes no more than that.
CREATE OR REPLACE FUNCTION record_node_status_change()
RETURNS TRIGGER AS $$
DECLARE
    last node_status_changes%ROWTYPE;
BEGIN
    IF (SELECT count(*) FROM node_status_changes c
        WHERE c.cluster_id = NEW.cluster_id AND c.node = NEW.name
          AND c.observed_at > now() - interval '1 hour') < 20 THEN
        INSERT INTO node_status_changes (cluster_id, node, from_status, to_status, last_seen_at)
        VALUES (NEW.cluster_id, NEW.name, OLD.status, NEW.status, OLD.last_seen_at);
    ELSE
        SELECT * INTO last FROM node_status_changes c
        WHERE c.cluster_id = NEW.cluster_id AND c.node = NEW.name
        ORDER BY c.observed_at DESC, c.id DESC LIMIT 1;
        IF last.from_status = NEW.status THEN
            DELETE FROM node_status_changes WHERE id = last.id;
        ELSE
            UPDATE node_status_changes SET to_status = NEW.status, observed_at = now() WHERE id = last.id;
        END IF;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- UPDATE OF status fires for UpsertNode's ON CONFLICT arm, which sets status
-- on every pass; the WHEN clause keeps an unchanged status from writing
-- anything. An INSERT records nothing: a new row has no earlier state.
DROP TRIGGER IF EXISTS trg_nodes_status_change ON nodes;
CREATE TRIGGER trg_nodes_status_change
    AFTER UPDATE OF status ON nodes
    FOR EACH ROW
    WHEN (OLD.status IS DISTINCT FROM NEW.status)
    EXECUTE FUNCTION record_node_status_change();

-- The tasks a node ran that have no audit row, newest first. task_history is
-- bounded by its retention, so a plain index builds quickly here.
CREATE INDEX IF NOT EXISTS idx_task_history_cluster_node_started
    ON task_history (cluster_id, node, started_at);
