-- 000080_guest_snapshots.up.sql
-- Central inventory of guest (QEMU/LXC) snapshots, collected periodically from
-- Proxmox so the UI can show every snapshot across all clusters and the alert
-- engine can evaluate snapshot age without per-request Proxmox fan-out.
--
-- Keyed on the Proxmox-stable (cluster_id, vmid, name) identity — deliberately
-- NOT on vms.id, which is minted anew whenever the collector churns a guest
-- row (see migrations 000068/000069). Cleanup rides ON DELETE CASCADE from
-- clusters; read paths LEFT JOIN vms at query time to expose the current row.
-- guest_type is persisted (not derived from vms) so vm-vs-container RBAC
-- filtering still works for rows whose guest has vanished.
--
-- Additive (new table only) — safe for in-place upgrade, no operator action.

CREATE TABLE IF NOT EXISTS guest_snapshots (
    cluster_id   UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    vmid         INT NOT NULL,
    name         TEXT NOT NULL,
    guest_type   TEXT NOT NULL DEFAULT 'qemu' CHECK (guest_type IN ('qemu', 'lxc')),
    node         TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    parent       TEXT NOT NULL DEFAULT '',
    vmstate      BOOLEAN NOT NULL DEFAULT false,
    snap_time    BIGINT NOT NULL DEFAULT 0,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, vmid, name)
);

COMMENT ON COLUMN guest_snapshots.vmstate IS 'RAM state included in the snapshot (QEMU only)';
COMMENT ON COLUMN guest_snapshots.snap_time IS 'Snapshot creation time as unix seconds; 0 = Proxmox omitted snaptime, age unknown';
