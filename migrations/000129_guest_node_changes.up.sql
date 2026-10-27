-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Additive: two new tables, two trigger functions and three triggers on vms.
-- The one backfill fills guest_node_state from vms as it stands — where each
-- guest is now — inside this migration's transaction, so an interrupted run
-- leaves nothing behind and a re-run starts over; no move before it is
-- invented.
--
-- Both back a node's History tab: the guests that arrived on the node and the
-- ones that left it (GET /api/v1/clusters/:cluster_id/nodes/:node_name/history).

-- guest_node_state is the node each guest of a cluster was last seen on, keyed
-- (cluster_id, vmid) as a guest is everywhere: it is what a move is measured
-- against. A trigger on vms.node_id alone would miss the commonest move — the
-- collector deletes a guest's row when a live migration hides it for a pass
-- and inserts a new one on the target — so the state outlives the row.
-- last_seen_at is when the guest was last seen on that node.
CREATE TABLE IF NOT EXISTS guest_node_state (
    cluster_id   UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    vmid         INTEGER NOT NULL,
    node         TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (cluster_id, vmid)
);

-- guest_node_changes records each move: the guest seen on to_node after
-- from_node. Node names are copied, as in node_status_changes. guest_type is
-- the vms.type, the move being inventory data read under view:vm or
-- view:container by kind. A VMID reused on another node after a destroy is no
-- move: the History leaves out a row with a create, restore, clone or destroy
-- of the VMID between last_seen_at and observed_at, at read time.
CREATE TABLE IF NOT EXISTS guest_node_changes (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id   UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    vmid         INTEGER NOT NULL,
    guest_type   TEXT NOT NULL,
    from_node    TEXT NOT NULL,
    to_node      TEXT NOT NULL,
    -- When the guest was last seen on from_node: it moved after this and by
    -- observed_at.
    last_seen_at TIMESTAMPTZ NOT NULL,
    observed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_guest_node_changes_from
    ON guest_node_changes (cluster_id, from_node, observed_at);
CREATE INDEX IF NOT EXISTS idx_guest_node_changes_to
    ON guest_node_changes (cluster_id, to_node, observed_at);

-- record_guest_node_change runs AFTER a vms row is inserted, or its node_id
-- changes: it records a move when the guest's last node differs, and keeps the
-- state. Kept to plain INSERTs a vms row cannot fail, as a failure here fails
-- the inventory write.
CREATE OR REPLACE FUNCTION record_guest_node_change()
RETURNS TRIGGER AS $$
DECLARE
    node_now text := COALESCE((SELECT n.name FROM nodes n WHERE n.id = NEW.node_id), '');
    was      guest_node_state%ROWTYPE;
BEGIN
    SELECT * INTO was FROM guest_node_state WHERE cluster_id = NEW.cluster_id AND vmid = NEW.vmid;
    IF FOUND AND was.node <> node_now AND was.node <> '' AND node_now <> '' THEN
        INSERT INTO guest_node_changes (cluster_id, vmid, guest_type, from_node, to_node, last_seen_at)
        VALUES (NEW.cluster_id, NEW.vmid, NEW.type, was.node, node_now,
                CASE WHEN TG_OP = 'UPDATE' THEN OLD.last_seen_at ELSE was.last_seen_at END);
    END IF;
    INSERT INTO guest_node_state (cluster_id, vmid, node, last_seen_at)
    VALUES (NEW.cluster_id, NEW.vmid, node_now, NEW.last_seen_at)
    ON CONFLICT (cluster_id, vmid) DO UPDATE SET node = EXCLUDED.node, last_seen_at = EXCLUDED.last_seen_at;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- A deleted row's last sighting is kept, so the row that replaces it on
-- another node dates the move from it rather than from the last one before.
CREATE OR REPLACE FUNCTION keep_guest_node_last_seen()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE guest_node_state SET last_seen_at = OLD.last_seen_at
    WHERE cluster_id = OLD.cluster_id AND vmid = OLD.vmid;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_vms_node_insert ON vms;
CREATE TRIGGER trg_vms_node_insert
    AFTER INSERT ON vms
    FOR EACH ROW
    EXECUTE FUNCTION record_guest_node_change();

-- UPDATE OF node_id fires for UpsertVM's ON CONFLICT arm, which sets node_id
-- on every pass; the WHEN clause keeps a guest that stayed put from writing.
DROP TRIGGER IF EXISTS trg_vms_node_change ON vms;
CREATE TRIGGER trg_vms_node_change
    AFTER UPDATE OF node_id ON vms
    FOR EACH ROW
    WHEN (OLD.node_id IS DISTINCT FROM NEW.node_id)
    EXECUTE FUNCTION record_guest_node_change();

DROP TRIGGER IF EXISTS trg_vms_node_delete ON vms;
CREATE TRIGGER trg_vms_node_delete
    AFTER DELETE ON vms
    FOR EACH ROW
    EXECUTE FUNCTION keep_guest_node_last_seen();

-- Where each guest is now: a move is measured from here on.
INSERT INTO guest_node_state (cluster_id, vmid, node, last_seen_at)
SELECT v.cluster_id, v.vmid, n.name, v.last_seen_at
FROM vms v
JOIN nodes n ON n.id = v.node_id
ON CONFLICT (cluster_id, vmid) DO NOTHING;
