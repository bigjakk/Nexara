-- 000004_inventory.up.sql
-- Inventory tables: nodes, vms, storage_pools

-- nodes
CREATE TABLE IF NOT EXISTS nodes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'unknown',
    cpu_count       INT NOT NULL DEFAULT 0,
    mem_total       BIGINT NOT NULL DEFAULT 0,
    disk_total      BIGINT NOT NULL DEFAULT 0,
    pve_version     TEXT NOT NULL DEFAULT '',
    ssl_fingerprint TEXT NOT NULL DEFAULT '',
    uptime          BIGINT NOT NULL DEFAULT 0,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE INDEX IF NOT EXISTS idx_nodes_cluster_id ON nodes (cluster_id);

-- vms (both QEMU VMs and LXC containers)
CREATE TABLE IF NOT EXISTS vms (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    node_id     UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    vmid        INT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    type        TEXT NOT NULL CHECK (type IN ('qemu', 'lxc')),
    status      TEXT NOT NULL DEFAULT 'unknown',
    cpu_count   INT NOT NULL DEFAULT 0,
    mem_total   BIGINT NOT NULL DEFAULT 0,
    disk_total  BIGINT NOT NULL DEFAULT 0,
    uptime      BIGINT NOT NULL DEFAULT 0,
    template    BOOLEAN NOT NULL DEFAULT false,
    tags        TEXT NOT NULL DEFAULT '',
    ha_state    TEXT NOT NULL DEFAULT '',
    pool        TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, vmid)
);

CREATE INDEX IF NOT EXISTS idx_vms_cluster_id ON vms (cluster_id);
CREATE INDEX IF NOT EXISTS idx_vms_node_id ON vms (node_id);

-- storage_pools
CREATE TABLE IF NOT EXISTS storage_pools (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    node_id     UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    storage     TEXT NOT NULL,
    type        TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL DEFAULT '',
    active      BOOLEAN NOT NULL DEFAULT false,
    enabled     BOOLEAN NOT NULL DEFAULT false,
    shared      BOOLEAN NOT NULL DEFAULT false,
    total       BIGINT NOT NULL DEFAULT 0,
    used        BIGINT NOT NULL DEFAULT 0,
    avail       BIGINT NOT NULL DEFAULT 0,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, node_id, storage)
);

CREATE INDEX IF NOT EXISTS idx_storage_pools_cluster_id ON storage_pools (cluster_id);
CREATE INDEX IF NOT EXISTS idx_storage_pools_node_id ON storage_pools (node_id);

-- updated_at triggers (function already exists from migration 1)
CREATE TRIGGER trg_nodes_updated_at
    BEFORE UPDATE ON nodes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_vms_updated_at
    BEFORE UPDATE ON vms
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_storage_pools_updated_at
    BEFORE UPDATE ON storage_pools
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
