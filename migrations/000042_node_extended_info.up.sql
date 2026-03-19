-- Extend nodes table with swap, DNS, timezone, subscription, and load avg data.
ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS swap_total          BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS swap_used           BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS swap_free           BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS dns_servers         TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS dns_search          TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS timezone            TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS subscription_status TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS subscription_level  TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS load_avg            TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS io_wait             DOUBLE PRECISION NOT NULL DEFAULT 0;

-- Physical disks discovered on each node.
CREATE TABLE IF NOT EXISTS node_disks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id     UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    dev_path    TEXT NOT NULL,
    model       TEXT NOT NULL DEFAULT '',
    serial      TEXT NOT NULL DEFAULT '',
    size        BIGINT NOT NULL DEFAULT 0,
    disk_type   TEXT NOT NULL DEFAULT '',
    health      TEXT NOT NULL DEFAULT '',
    wearout     TEXT NOT NULL DEFAULT '',
    rpm         INT  NOT NULL DEFAULT 0,
    vendor      TEXT NOT NULL DEFAULT '',
    wwn         TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (node_id, dev_path)
);

-- Network interfaces discovered on each node.
CREATE TABLE IF NOT EXISTS node_network_interfaces (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id      UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    cluster_id   UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    iface        TEXT NOT NULL,
    iface_type   TEXT NOT NULL DEFAULT '',
    active       BOOLEAN NOT NULL DEFAULT false,
    autostart    BOOLEAN NOT NULL DEFAULT false,
    method       TEXT NOT NULL DEFAULT '',
    method6      TEXT NOT NULL DEFAULT '',
    address      TEXT NOT NULL DEFAULT '',
    netmask      TEXT NOT NULL DEFAULT '',
    gateway      TEXT NOT NULL DEFAULT '',
    cidr         TEXT NOT NULL DEFAULT '',
    bridge_ports TEXT NOT NULL DEFAULT '',
    comments     TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (node_id, iface)
);

-- PCI devices discovered on each node.
CREATE TABLE IF NOT EXISTS node_pci_devices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id         UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    pci_id          TEXT NOT NULL,
    class           TEXT NOT NULL DEFAULT '',
    device_name     TEXT NOT NULL DEFAULT '',
    vendor_name     TEXT NOT NULL DEFAULT '',
    device          TEXT NOT NULL DEFAULT '',
    vendor          TEXT NOT NULL DEFAULT '',
    iommu_group     INT  NOT NULL DEFAULT -1,
    subsystem_device TEXT NOT NULL DEFAULT '',
    subsystem_vendor TEXT NOT NULL DEFAULT '',
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (node_id, pci_id)
);
