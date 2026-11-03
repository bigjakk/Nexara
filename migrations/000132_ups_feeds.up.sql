-- UPS monitoring, Phase 2a: what each UPS powers, and which upsmon clients are
-- logged in to it.
--
-- Purely additive: two new tables and two nullable columns on nut_servers.
-- Nothing that exists is altered, so an upgrade needs no operator action.
--
-- ups_clients  the logins of a UPS's latest LIST CLIENT, one row per address
-- ups_feeds    the nodes and other loads a UPS powers, found from those logins
--              or added by hand

CREATE TABLE IF NOT EXISTS ups_clients (
    ups_id        UUID NOT NULL REFERENCES ups_devices(id) ON DELETE CASCADE,
    address       TEXT NOT NULL,
    connections   SMALLINT NOT NULL DEFAULT 1 CHECK (connections >= 0),
    missed        SMALLINT NOT NULL DEFAULT 0 CHECK (missed >= 0),
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (ups_id, address)
);

COMMENT ON COLUMN ups_clients.address IS 'A logged-in connection''s address as upsd reports it, with an IPv6-mapped IPv4 address unmapped. upsmon only appears here once its LOGIN succeeds';
COMMENT ON COLUMN ups_clients.connections IS 'How many lines the address had in the latest listing, 0 when it was missing: upsd lists each connection, so two upsmons behind one NAT, or a primary and a secondary on one host, are two';
COMMENT ON COLUMN ups_clients.missed IS 'Listings in a row the address has been missing from. A node turns unprotected only at 2: a vanished host stays listed up to 60 s, and a restarting upsmon is briefly absent';

CREATE TABLE IF NOT EXISTS ups_feeds (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ups_id     UUID NOT NULL REFERENCES ups_devices(id) ON DELETE CASCADE,
    cluster_id UUID REFERENCES clusters(id) ON DELETE CASCADE,
    node_name  TEXT,
    label      TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL CHECK (source IN ('found', 'manual')),
    dismissed  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    -- A feed is a node (cluster and name) or a load that is not one, such as
    -- a switch (a label). Only a node can be found from a login.
    CHECK ((cluster_id IS NOT NULL AND node_name IS NOT NULL AND node_name <> '' AND label = '')
        OR (cluster_id IS NULL AND node_name IS NULL AND label <> '' AND source = 'manual'))
);

COMMENT ON TABLE ups_feeds IS 'What a UPS powers. Keyed on the node''s name, like node_status_changes, so a node row that is re-created keeps its feeds';
COMMENT ON COLUMN ups_feeds.source IS 'found: a upsmon login came from the node''s address. Never removed automatically, so a node that stops logging in reads not protected rather than not fed. manual: added by hand';
COMMENT ON COLUMN ups_feeds.dismissed IS 'A node feed the operator removed. Kept so a login from the node does not find it again; adding the node by hand revives it. Label feeds are deleted instead';
COMMENT ON COLUMN ups_feeds.created_by IS 'Who added a manual feed; NULL for a found one';

CREATE UNIQUE INDEX IF NOT EXISTS idx_ups_feeds_node ON ups_feeds (ups_id, cluster_id, node_name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ups_feeds_label ON ups_feeds (ups_id, lower(label)) WHERE label <> '';

-- The node a NUT server runs on, when it is one: found from the server's own
-- address, or set by hand. No CHECK pairs the two columns, because deleting a
-- cluster sets only host_cluster_id to NULL; a host_node_name without it means
-- nothing and is overwritten when the host is next set.
ALTER TABLE nut_servers ADD COLUMN IF NOT EXISTS host_cluster_id UUID REFERENCES clusters(id) ON DELETE SET NULL;
ALTER TABLE nut_servers ADD COLUMN IF NOT EXISTS host_node_name TEXT;
