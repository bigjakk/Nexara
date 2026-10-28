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
-- against. A move is mostly an UPDATE of vms.node_id; the state outlives a
-- deleted row too, for a guest the collector deleted and inserted again.
-- last_seen_at is when the guest was last seen on that node, and gone_at when
-- its row was deleted (NULL while it has one).
CREATE TABLE IF NOT EXISTS guest_node_state (
    cluster_id   UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    vmid         INTEGER NOT NULL,
    node         TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    gone_at      TIMESTAMPTZ,
    PRIMARY KEY (cluster_id, vmid)
);

-- guest_node_changes records each move: the guest seen on to_node after
-- from_node. Node names are copied, as in node_status_changes. guest_type is
-- the vms.type, the move being inventory data read under view:vm or
-- view:container by kind. A VMID reused on another node after a destroy is no
-- move: no row is recorded for a guest back more than five minutes after its
-- row went (record_guest_node_change), and the History leaves out one with a
-- create, restore, clone or destroy of the VMID on record between last_seen_at
-- and observed_at, at read time.
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
CREATE INDEX IF NOT EXISTS idx_guest_node_changes_guest
    ON guest_node_changes (cluster_id, vmid, observed_at);

-- record_guest_node_change runs AFTER a vms row is inserted, or its node_id
-- changes: it records a move when the guest's last node differs, and keeps the
-- state. Kept to statements a vms row cannot fail, as a failure here fails
-- the inventory write.
--
-- A row inserted after its predecessor was deleted is the same guest only
-- within five minutes of the delete. The collector deletes a row only after
-- the guest went unseen for five minutes (staleVMGrace), so a live
-- migration's one unseen pass never comes this way; a guest back on another
-- node within one more such span — its new node's syncs failing meanwhile —
-- moved, and a guest on the VMID any later is a new one: a destroy and a
-- create or a clone into it, which Proxmox records as no move.
--
-- Twenty moves an hour a guest at most, as the observer caps its changes: past
-- that, the hour's last row takes the move in — it ends where the guest now is
-- — and a move straight back cancels it. A guest moved round and round writes
-- no more than that.
CREATE OR REPLACE FUNCTION record_guest_node_change()
RETURNS TRIGGER AS $$
DECLARE
    node_now text := COALESCE((SELECT n.name FROM nodes n
                               WHERE n.id = NEW.node_id AND n.cluster_id = NEW.cluster_id), '');
    was      guest_node_state%ROWTYPE;
    last     guest_node_changes%ROWTYPE;
BEGIN
    SELECT * INTO was FROM guest_node_state WHERE cluster_id = NEW.cluster_id AND vmid = NEW.vmid;
    IF FOUND AND was.node <> node_now AND was.node <> '' AND node_now <> ''
       AND (TG_OP = 'UPDATE' OR was.gone_at > now() - interval '5 minutes') THEN
        IF (SELECT count(*) FROM guest_node_changes c
            WHERE c.cluster_id = NEW.cluster_id AND c.vmid = NEW.vmid
              AND c.observed_at > now() - interval '1 hour') < 20 THEN
            INSERT INTO guest_node_changes (cluster_id, vmid, guest_type, from_node, to_node, last_seen_at)
            VALUES (NEW.cluster_id, NEW.vmid, NEW.type, was.node, node_now,
                    CASE WHEN TG_OP = 'UPDATE' THEN OLD.last_seen_at ELSE was.last_seen_at END);
        ELSE
            SELECT * INTO last FROM guest_node_changes c
            WHERE c.cluster_id = NEW.cluster_id AND c.vmid = NEW.vmid
            ORDER BY c.observed_at DESC, c.id DESC LIMIT 1;
            IF last.from_node = node_now THEN
                DELETE FROM guest_node_changes WHERE id = last.id;
            ELSE
                UPDATE guest_node_changes SET to_node = node_now, observed_at = now() WHERE id = last.id;
            END IF;
        END IF;
    END IF;
    INSERT INTO guest_node_state (cluster_id, vmid, node, last_seen_at)
    VALUES (NEW.cluster_id, NEW.vmid, node_now, NEW.last_seen_at)
    ON CONFLICT (cluster_id, vmid) DO UPDATE
    SET node = EXCLUDED.node, last_seen_at = EXCLUDED.last_seen_at, gone_at = NULL;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- A deleted row's last sighting is kept, and when it went, so a row that
-- replaces it on another node soon after dates the move from the sighting,
-- and one that comes later is no move at all.
CREATE OR REPLACE FUNCTION keep_guest_node_last_seen()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE guest_node_state SET last_seen_at = OLD.last_seen_at, gone_at = now()
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
