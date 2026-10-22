-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Adds the table that keeps each node's last memory DIMM read.
--
-- A new table only; nothing existing changes. Rows appear as the daily read,
-- or a node page's Read now, reaches each node with `dmidecode` over its
-- cluster's SSH credentials. A failed read updates status, reason and
-- attempted_at and keeps the last good read. A node's row goes with the node.

CREATE TABLE IF NOT EXISTS node_memory_inventory (
    node_id            UUID PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    status             TEXT NOT NULL CHECK (status IN ('ok', 'unavailable', 'error')),
    reason             TEXT NOT NULL DEFAULT '',
    attempted_at       TIMESTAMPTZ NOT NULL,
    read_at            TIMESTAMPTZ,
    installed_bytes    BIGINT NOT NULL DEFAULT 0,
    slots_used         INTEGER NOT NULL DEFAULT 0,
    slots_total        INTEGER NOT NULL DEFAULT 0,
    max_capacity_bytes BIGINT NOT NULL DEFAULT 0,
    ecc                TEXT NOT NULL DEFAULT '',
    modules            JSONB NOT NULL DEFAULT '[]'
);

COMMENT ON TABLE node_memory_inventory IS 'Each node''s last memory DIMM read (dmidecode over SSH). A failed read updates status, reason and attempted_at and keeps the last good read.';
COMMENT ON COLUMN node_memory_inventory.max_capacity_bytes IS 'The firmware''s own Maximum Capacity, summed over the system memory arrays; boards often misstate it';
