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
-- last_seen_at is when the guest was last seen on that node, gone_at when its
-- row was deleted (NULL while it has one), and guest_type what it was.
CREATE TABLE IF NOT EXISTS guest_node_state (
    cluster_id   UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    vmid         INTEGER NOT NULL,
    node         TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    gone_at      TIMESTAMPTZ,
    guest_type   TEXT NOT NULL DEFAULT '',
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
    observed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 0 for a move; for a run — the moves past the hourly cap, in one row — how
    -- many, from_node the first one's, to_node where they ended, nodes every
    -- node it went through, last_seen_at the first one's time and observed_at
    -- the last one's.
    run_count    INTEGER NOT NULL DEFAULT 0,
    nodes        TEXT[] NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_guest_node_changes_from
    ON guest_node_changes (cluster_id, from_node, observed_at);
CREATE INDEX IF NOT EXISTS idx_guest_node_changes_to
    ON guest_node_changes (cluster_id, to_node, observed_at);
CREATE INDEX IF NOT EXISTS idx_guest_node_changes_guest
    ON guest_node_changes (cluster_id, vmid, observed_at);
-- The runs, which a node's History reads by the nodes they went through
-- (nodes holds them, no index the read can range over): only this.
CREATE INDEX IF NOT EXISTS idx_guest_node_changes_runs
    ON guest_node_changes (cluster_id, observed_at) WHERE run_count > 0;

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
-- create or a clone into it, which Proxmox records as no move. A guest of
-- another type on the VMID — a container where a VM was — is a new one too,
-- however soon. Whether a move was a migration is the History's to say, at
-- read time: a new guest on a VMID the collector had not yet let go of comes
-- as an UPDATE of node_id, like a move.
--
-- Twenty moves an hour a guest, as the observer caps its changes; past that,
-- the burst's run row counts the rest — its first, its last, how many and
-- where they ended — so a guest moved round and round writes one row an hour
-- more, and no move is lost or rewritten.
CREATE OR REPLACE FUNCTION record_guest_node_change()
RETURNS TRIGGER AS $$
DECLARE
    node_now text := COALESCE((SELECT n.name FROM nodes n
                               WHERE n.id = NEW.node_id AND n.cluster_id = NEW.cluster_id), '');
    was      guest_node_state%ROWTYPE;
    type_was text;
    run      guest_node_changes%ROWTYPE;
BEGIN
    SELECT * INTO was FROM guest_node_state WHERE cluster_id = NEW.cluster_id AND vmid = NEW.vmid;
    -- Not in the IF below: PL/pgSQL would end its condition at a CASE's THEN.
    type_was := COALESCE(OLD.type, was.guest_type);
    IF FOUND AND was.node <> node_now AND was.node <> '' AND node_now <> '' AND NEW.type = type_was
       AND (TG_OP = 'UPDATE' OR was.gone_at > now() - interval '5 minutes') THEN
        IF (SELECT count(*) FROM guest_node_changes c
            WHERE c.cluster_id = NEW.cluster_id AND c.vmid = NEW.vmid AND c.run_count = 0
              AND c.observed_at > now() - interval '1 hour') < 20 THEN
            INSERT INTO guest_node_changes (cluster_id, vmid, guest_type, from_node, to_node, last_seen_at)
            VALUES (NEW.cluster_id, NEW.vmid, NEW.type, was.node, node_now,
                    CASE WHEN TG_OP = 'UPDATE' THEN OLD.last_seen_at ELSE was.last_seen_at END);
        ELSE
            SELECT * INTO run FROM guest_node_changes c
            WHERE c.cluster_id = NEW.cluster_id AND c.vmid = NEW.vmid AND c.run_count > 0
              AND c.observed_at > now() - interval '1 hour'
            ORDER BY c.observed_at DESC, c.id DESC LIMIT 1;
            IF FOUND THEN
                UPDATE guest_node_changes
                SET to_node = node_now, observed_at = now(), run_count = run.run_count + 1,
                    nodes = CASE WHEN node_now = ANY(run.nodes) THEN run.nodes ELSE run.nodes || node_now END
                WHERE id = run.id;
            ELSE
                INSERT INTO guest_node_changes (cluster_id, vmid, guest_type, from_node, to_node, last_seen_at,
                                                run_count, nodes)
                VALUES (NEW.cluster_id, NEW.vmid, NEW.type, was.node, node_now, now(), 1, ARRAY[was.node, node_now]);
            END IF;
        END IF;
    END IF;
    INSERT INTO guest_node_state (cluster_id, vmid, node, last_seen_at, guest_type)
    VALUES (NEW.cluster_id, NEW.vmid, node_now, NEW.last_seen_at, NEW.type)
    ON CONFLICT (cluster_id, vmid) DO UPDATE
    SET node = EXCLUDED.node, last_seen_at = EXCLUDED.last_seen_at, gone_at = NULL,
        guest_type = EXCLUDED.guest_type;
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
INSERT INTO guest_node_state (cluster_id, vmid, node, last_seen_at, guest_type)
SELECT v.cluster_id, v.vmid, n.name, v.last_seen_at, v.type
FROM vms v
JOIN nodes n ON n.id = v.node_id
ON CONFLICT (cluster_id, vmid) DO NOTHING;
