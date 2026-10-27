-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Purely additive: one new table, and a column with a default on
-- cluster_object_baselines (000124). Nothing existing is read,
-- rewritten or backfilled, so an interrupted run leaves nothing behind — the
-- migration is one transaction.
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
-- last_seen_at moves on every pass for every object, so the table is filled to
-- 70% to leave room for those updates on the page (no index covers the column,
-- so they can be heap-only).
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

-- The signature a kind was last read whole under (when is the table's own
-- last_read_at). An object that appears in a kind read before under the same
-- signature was created after last_read_at; one that appears in a kind never
-- read, or read under another signature, is part of a baseline. The
-- replication trigger's mark (000125) and every row from before this migration
-- have no signature, which matches no reading.
ALTER TABLE cluster_object_baselines
    ADD COLUMN IF NOT EXISTS signature TEXT NOT NULL DEFAULT '';
