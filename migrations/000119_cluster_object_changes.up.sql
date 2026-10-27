-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Purely additive: two new tables and their indexes. Nothing existing is read,
-- rewritten or backfilled, so an interrupted run leaves nothing behind — the
-- migration is one transaction.
--
-- cluster_object_changes records a change to an object that reaches no audit
-- row: a replication job edited in the Proxmox GUI, an OSD that went down, a
-- pool whose size was changed there, a guest's configuration edited there. It
-- is guest_status_changes (000115) for objects that are not a guest's status,
-- and keyed the way audit_log is — cluster_id, resource_type, resource_id,
-- spelled exactly as the audit rows for the same object spell them
-- (replication / <job id>, ceph_osd / <osd number>, ceph_pool / <name>; a guest
-- is vm / <its VMID as text> with vmid beside it as a number, because vms.id is
-- re-issued whenever a guest's row is re-created and cannot key a record that
-- has to outlive it) — so a History reads both with one selector.
--
-- Three writers file into it, told apart by observed_by: 'trigger' (the trigger
-- on replication_jobs, 000120), 'ceph' (the collector's Ceph pass, which diffs
-- one reading against the last) and 'observer' (the diff of what Proxmox reports
-- against what Nexara last wrote, for the configuration of objects edited
-- directly in Proxmox). observed_by says how a change was seen; it is not
-- audit_log.source, which says who made the change.
--
-- The facts are never updated. `explained` is NULL until the collector has had
-- time to ingest the task that may explain the change, then true or false, once
-- (ClassifyClusterObjectChanges, queries/cluster_object_changes.sql): a change is
-- explained when an audit row for the same object, of an action that does what
-- changed (an OSD that went down by osd_stop, a pool's size by update ...), lies
-- in its window, and `explained_by` is that audit row's id. The observer's rows
-- are decided by the observer, with rules per kind of object. Only a row
-- classified false is shown; an explained one stays hidden and is KEPT, with the
-- id of the row that explained it, for as long as an unexplained one is.
--
-- Nothing prunes this table, as nothing prunes audit_log (the audit log has no
-- retention; the guest History keeps its rows as long as it does). What writes to it
-- is bounded: the Ceph pass files at most 20 rows an hour for each field of each
-- object, and a replication job adds a row per edit made to it.
CREATE TABLE IF NOT EXISTS cluster_object_changes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id    UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL,
    resource_id   TEXT NOT NULL,
    -- A guest's VMID as a number, beside its resource_id (the same VMID as
    -- text); NULL for every other object. audit_log.vmid (000084) is the same
    -- denormalisation of the same fact.
    vmid          INTEGER,
    change        TEXT NOT NULL CHECK (change IN ('added', 'removed', 'changed')),
    -- changed: the names of the fields that differ. added / removed: empty.
    -- Names only, so a writer that must not store a value (a guest's password
    -- fields) still says what was edited.
    fields        TEXT[] NOT NULL DEFAULT '{}',
    -- added / removed: a snapshot of the object. changed: each field that
    -- differs, as {"from": ..., "to": ...}. Only fields whose values the
    -- object's own view permission already shows; may be empty.
    detail        JSONB NOT NULL DEFAULT '{}',
    -- The node NAME at the time (an OSD's host), copied rather than referenced:
    -- the classification waits for that node's task list, and the record must
    -- outlive the node. Empty for an object no node owns.
    node          TEXT NOT NULL DEFAULT '',
    observed_by   TEXT NOT NULL CHECK (observed_by IN ('trigger', 'ceph', 'observer')),
    -- When the object was last seen as it was: the change happened after this
    -- and by observed_at. Normally one slow pass apart; after Nexara or a node
    -- was unreachable, the whole gap.
    last_seen_at  TIMESTAMPTZ NOT NULL,
    observed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    explained     BOOLEAN,
    -- The audit row that explained the change; NULL until then, and for a row
    -- explained by an observer that names none. No foreign key: the pointer must
    -- never hold an audit row (or its cluster) back.
    explained_by  UUID
);

-- One object's History, and a kind's.
CREATE INDEX IF NOT EXISTS idx_cluster_object_changes_object
    ON cluster_object_changes (cluster_id, resource_type, resource_id, observed_at);

-- A kind's or an object's changes across clusters. The index above leads with
-- cluster_id, which a generic plan cannot use under ($global OR cluster_id = $c)
-- and a global kind has no predicate for at all; unexplained rows are kept, so
-- without this both would scan the whole table.
CREATE INDEX IF NOT EXISTS idx_cluster_object_changes_kind
    ON cluster_object_changes (resource_type, resource_id, observed_at);

-- What a kind's History lists: the changes nothing explained, a small part of the
-- table (ListClusterObjectChanges), newest first.
CREATE INDEX IF NOT EXISTS idx_cluster_object_changes_shown
    ON cluster_object_changes (cluster_id, observed_at DESC)
    WHERE explained = false;

-- A guest's History, which survives its vms row being re-created.
CREATE INDEX IF NOT EXISTS idx_cluster_object_changes_guest
    ON cluster_object_changes (cluster_id, vmid, observed_at)
    WHERE vmid IS NOT NULL;

-- The classification pass's working set: small, because a row leaves it once
-- classified.
CREATE INDEX IF NOT EXISTS idx_cluster_object_changes_unclassified
    ON cluster_object_changes (cluster_id, observed_at)
    WHERE explained IS NULL;

-- cluster_object_baselines says that a cluster's objects of one kind have been
-- read at least once. A trigger records an object appearing only after that:
-- the first read of a cluster just added lists everything that was always
-- there, and none of it is a change.
-- last_read_at is when the last pass finished reading them: an object that
-- appears now was absent then, so it is the change's last_seen_at.
-- reading is the last reading itself, for a kind whose objects are not rows of a
-- table of their own (a cluster's Ceph OSDs and pools): the previous reading is
-- what the next pass diffs against, so it is kept here and a restart of Nexara
-- compares against it instead of starting over — what changed while it was down is
-- recorded, and so is a change the hourly cap was still holding back (its field
-- stays at the reading it was last recorded at). NULL for a kind that has its own
-- table (replication).
CREATE TABLE IF NOT EXISTS cluster_object_baselines (
    cluster_id    UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL,
    baselined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_read_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    reading       JSONB,
    PRIMARY KEY (cluster_id, resource_type)
);
