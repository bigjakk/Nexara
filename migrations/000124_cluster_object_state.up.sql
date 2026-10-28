-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Purely additive: two new tables, a column with a default on
-- cluster_object_baselines (000119), two with a default and one nullable on
-- cluster_object_changes (000119), a partial index on task_history and a
-- partial one on cluster_object_changes (each built in the migration's
-- transaction, a lock on its table for as long as the build takes; the second
-- indexes no existing row). Nothing existing is rewritten or backfilled, so an
-- interrupted run leaves nothing behind — the migration is one transaction.
--
-- The observer (internal/collector/observer.go) records edits made directly in
-- Proxmox that reach no audit row: a guest's configuration, a pool, a storage
-- definition, a node's settings, edited in the Proxmox GUI or API without a
-- task. It finds them by comparing what Proxmox reports with what it last read,
-- and what it last read is kept here rather than in memory, so a restart does
-- not take a fresh baseline and an edit made while Nexara was down is still
-- seen, in a window that starts when the object was last read.
--
-- Never a value. `fields` maps each field Proxmox reported to an HMAC tag of its
-- value (crypto.Tag: a key derived from ENCRYPTION_KEY, bound to the cluster,
-- the object and the field), or, for an ordered list such as a firewall rule
-- set, to the list of its items' tags. A changed value changes the tag; without
-- the key, which is not in the database, the tag says nothing about the value —
-- so a guest's cloud-init password, an IPAM token or a node's notes cannot be
-- read or guessed from a dump, as a plain digest of a short secret could. No
-- API reads this table.
--
-- kind and object_id are the observer's own key (kind "vm", object "100";
-- "node_dns", "pve-01"), which is not always the audit identity a change is
-- filed under (both of those are filed as resource_type node).
--
-- signature names what the tags were taken under: the key, the Proxmox versions
-- that answered, and the privileges of the cluster's token. An object read under
-- another signature is taken as a fresh baseline and records nothing: an
-- upgrade changes what Proxmox reports without anyone editing anything.
--
-- last_seen_at moves on every pass for a node's objects, and for those of a
-- reading that left one of its kind unread (a cluster's kind read whole moves
-- its mark instead, cluster_object_baselines), so the table is filled to 70% to
-- leave room for those updates on the page (no index covers the column, so
-- they can be heap-only).
CREATE TABLE IF NOT EXISTS cluster_object_state (
    cluster_id    UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    object_id     TEXT NOT NULL,
    fields        JSONB NOT NULL,
    node          TEXT NOT NULL DEFAULT '',
    signature     TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- When the read began that last saw the object as stored: a change found by
    -- the next read happened after this.
    last_seen_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (cluster_id, kind, object_id)
) WITH (fillfactor = 70);

-- What the observer holds of an object it compares past its kind's cap (or its
-- parent's): its id and one tag of the whole object, written only when the
-- object appears, changes or goes, never on a pass that finds it as it was.
-- Such a kind's changes are counted, not filed one by one
-- (internal/collector/observer.go, apply), and its objects move back to
-- cluster_object_state, field by field, when it is under the cap again.
CREATE TABLE IF NOT EXISTS cluster_object_presence (
    cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    object_id  TEXT NOT NULL,
    node       TEXT NOT NULL DEFAULT '',
    tag        TEXT NOT NULL,
    signature  TEXT NOT NULL,
    PRIMARY KEY (cluster_id, kind, object_id)
);

-- The signature a kind was last read whole under (when is the table's own
-- last_read_at). An object that appears in a kind read before under the same
-- signature was created after last_read_at; one that appears in a kind never
-- read, or read under another signature, is part of a baseline. The
-- replication trigger's mark (000120) and every row from before this migration
-- have no signature, which matches no reading.
ALTER TABLE cluster_object_baselines
    ADD COLUMN IF NOT EXISTS signature TEXT NOT NULL DEFAULT '';

-- The members that joined or left a list of other objects' ids in a change the
-- observer filed, as "<field>:<id>": a pool's guests and storage, a backup
-- job's guests, an HA rule's resources, a user's groups, a group's users. For
-- an object added every member joined, for one removed every member left. They
-- are ids, not values, and no reader selects them: the classification
-- (ClassifyObservedChanges) needs them so that a guest's destroy explains only
-- the jobs, pools and rules that guest left.
--
-- The observer's rows are kept, explained or not, as long as their cluster:
-- one explained names its explanation (explained_by), which a History shows
-- beside the audit row, so nothing prunes them. They grow by about one row for
-- each edit Nexara makes to an observed object — the observer sees it too, and
-- files it explained — and one for each edit made in Proxmox.
ALTER TABLE cluster_object_changes
    ADD COLUMN IF NOT EXISTS members_joined TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS members_left   TEXT[] NOT NULL DEFAULT '{}';

-- Of a change something explained, each explaining audit row's id and the
-- fields it covered without naming them, {"<audit id>": ["<field>", ...]}: what
-- Proxmox derives from what the row sent, a disk's unused<n> and boot beside
-- the disk it names, every field of a task that names none (qmconfig), and
-- anything of another object than the row's own (a guest's destroy leaving its
-- pools). A History shows them beside that row as also changed in Proxmox. A
-- field the row names — where its rule reads the fields it set, as the rule
-- reads them (the classifiers' row_names) — is not here, nor anything of a
-- change nothing explained; NULL for nothing. Written by the classifiers
-- (ClassifyObservedChanges, ClassifyGlobalObjectChanges,
-- ClassifyClusterObjectChanges), each for a change once, as it decides it.
ALTER TABLE cluster_object_changes ADD COLUMN IF NOT EXISTS explained_unnamed JSONB;

-- A page of audit rows reads what they covered unnamed by their ids:
-- explained_unnamed ?| <ids>. No existing row has any, so the build reads the
-- table and indexes nothing.
CREATE INDEX IF NOT EXISTS idx_cluster_object_changes_explained_unnamed
    ON cluster_object_changes USING gin (explained_unnamed) WHERE explained_unnamed IS NOT NULL;

-- A realm sync's removals are explained by its own log (ClassifyObservedChanges'
-- task_logs), and the collector finds the syncs whose logs it reads by this:
-- the day's tasks of one worker type, a handful among thousands.
CREATE INDEX IF NOT EXISTS idx_task_history_realm_syncs
    ON task_history (cluster_id, started_at) WHERE task_type = 'auth-realm-sync';
