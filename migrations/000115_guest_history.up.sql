-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Purely additive: two new tables, one trigger function and one trigger on vms.
-- Nothing existing is read, rewritten or backfilled, so an interrupted run
-- leaves nothing behind — the migration is one transaction.
--
-- Both tables back a guest's History tab
-- (GET /api/v1/clusters/:cluster_id/guests/:vmid/history, queries/guest_history.sql).
-- Each closes a hole that left the history with nothing to read:
--
--   1. A task's OUTCOME was gone a week after it ran. audit_log is kept forever
--      but written once, at dispatch, and never updated (handlers/common.go), so
--      it cannot say how the task ended; task_history can, and it is pruned
--      after TASK_HISTORY_RETENTION (7 days by default).
--   2. A guest that stopped with no task — a shutdown from inside the guest, a
--      crash, an OOM kill — left no durable trace at all. The collector only
--      published the change on the live event stream.

-- task_history_archive keeps a guest task's row once DeleteCompletedTasks
-- (queries/tasks.sql) prunes it from task_history. The prune MOVES the row, in
-- the same statement, so a UPID is never in both tables and a reader can take
-- whichever one holds it.
--
-- Guest tasks only, decided by is_guest_upid() (000084): the History tab is
-- the only reader. vmid is therefore never NULL here, unlike task_history's.
--
-- user_id is SET NULL rather than CASCADE, unlike task_history's: deleting a
-- user must not delete the record of what happened to a guest.
CREATE TABLE IF NOT EXISTS task_history_archive (
    upid        TEXT PRIMARY KEY,
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    vmid        INTEGER NOT NULL,
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL,
    exit_status TEXT NOT NULL DEFAULT '',
    node        TEXT NOT NULL DEFAULT '',
    task_type   TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'nexara',
    started_at  TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ
);

-- finished_at last, for ClassifyGuestStatusChanges: a change only searches the
-- archive from its own window on, so a recent change reads none of it.
CREATE INDEX IF NOT EXISTS idx_task_history_archive_cluster_vmid
    ON task_history_archive (cluster_id, vmid, finished_at);

-- guest_status_changes records each change of a guest's run state, as the
-- inventory row saw it. The trigger below writes it, so every writer of
-- vms.status is covered without remembering to: the fast and slow collector
-- passes (UpsertVM) and the task watcher (UpdateVMStatus).
--
-- Most changes are a task's doing, and the task is already in the history.
-- `explained` says which: NULL until the collector has had time to ingest the
-- task that may explain the change, then true or false, once
-- (ClassifyGuestStatusChanges, queries/guest_history.sql). Only a row
-- classified false is shown. The facts themselves — the two states, the node,
-- the time — are never updated.
--
-- node is the node NAME at the time, copied rather than referenced: node rows
-- are pruned with the node, and the record must outlive it. guest_type is the
-- vms.type (qemu or lxc), because the run state is inventory data and is read
-- under view:vm or view:container by kind (handlers/guest_history.go).
CREATE TABLE IF NOT EXISTS guest_status_changes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    vmid        INTEGER NOT NULL,
    guest_type  TEXT NOT NULL,
    node        TEXT NOT NULL DEFAULT '',
    from_status TEXT NOT NULL,
    to_status   TEXT NOT NULL,
    -- When a sync last wrote the row in from_status: the change happened after
    -- this and by observed_at. Normally one slow pass apart; after Nexara or a
    -- node was unreachable, the whole gap.
    last_seen_at TIMESTAMPTZ NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    explained   BOOLEAN
);

CREATE INDEX IF NOT EXISTS idx_guest_status_changes_cluster_vmid
    ON guest_status_changes (cluster_id, vmid, observed_at);

-- The classification pass's working set: small, because a row leaves it once
-- classified.
CREATE INDEX IF NOT EXISTS idx_guest_status_changes_unclassified
    ON guest_status_changes (cluster_id, observed_at)
    WHERE explained IS NULL;

-- record_guest_status_change runs AFTER the row is written, so a failure here
-- fails the inventory write that caused it. It is kept to one INSERT with no
-- constraint the trigger's own row can violate for that reason: cluster_id,
-- vmid and type come from a vms row that already satisfies them.
CREATE OR REPLACE FUNCTION record_guest_status_change()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO guest_status_changes (cluster_id, vmid, guest_type, node, from_status, to_status, last_seen_at)
    VALUES (NEW.cluster_id, NEW.vmid, NEW.type,
            COALESCE((SELECT n.name FROM nodes n WHERE n.id = NEW.node_id), ''),
            OLD.status, NEW.status, OLD.last_seen_at);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- UPDATE OF status fires for the ON CONFLICT DO UPDATE arm of UpsertVM too,
-- which lists status in its SET; the WHEN clause keeps an unchanged status —
-- every quiet sync pass — from writing anything. An INSERT records nothing: a
-- new row has no earlier state to change from.
DROP TRIGGER IF EXISTS trg_vms_status_change ON vms;
CREATE TRIGGER trg_vms_status_change
    AFTER UPDATE OF status ON vms
    FOR EACH ROW
    WHEN (OLD.status IS DISTINCT FROM NEW.status)
    EXECUTE FUNCTION record_guest_status_change();
