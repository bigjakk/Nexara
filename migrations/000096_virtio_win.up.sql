-- 000096_virtio_win.up.sql
-- Automatic virtio-win ISO acquisition: an upstream release catalog, a
-- per-cluster download policy, and a job history for reconciling the
-- download-url UPIDs Proxmox hands back.
--
-- Three tables, three different lifetimes, which is why they are not one:
--   virtio_win_releases  — global, mirrors what upstream publishes. Not
--                          per-cluster: every cluster sees the same catalog,
--                          and a release row outlives any download of it.
--   virtio_win_configs   — per-cluster policy. Follows the drs_configs /
--                          cve_scan_schedules precedent rather than the
--                          settings table, whose 'cluster' scope is rejected
--                          outright by the settings handlers.
--   virtio_win_downloads — one row per dispatched download, keyed by UPID so
--                          the reconcile loop can finish what the scheduler
--                          started across a restart.
--
-- On the version/filename split: upstream directory "virtio-win-0.1.302-1"
-- contains "virtio-win-0.1.302.iso" — the ISO filename drops the release
-- suffix. Both forms are stored rather than derived at read time so a future
-- upstream naming change cannot retroactively break URLs already recorded.
--
-- Additive (new tables only) — safe for in-place upgrade, no operator action.

-- Upstream release catalog, refreshed by the scheduler's virtio-win check.
CREATE TABLE IF NOT EXISTS virtio_win_releases (
    version            TEXT PRIMARY KEY,
    iso_version        TEXT NOT NULL,
    iso_filename       TEXT NOT NULL,
    iso_url            TEXT NOT NULL,
    iso_size           BIGINT NOT NULL DEFAULT 0,
    is_stable          BOOLEAN NOT NULL DEFAULT false,
    checksum           TEXT NOT NULL DEFAULT '',
    checksum_algorithm TEXT NOT NULL DEFAULT '',
    published_at       TIMESTAMPTZ,
    discovered_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN virtio_win_releases.version IS 'Upstream directory version including the release suffix, e.g. "0.1.302-1"';
COMMENT ON COLUMN virtio_win_releases.iso_version IS 'Version as it appears in the ISO filename, i.e. version without the release suffix ("0.1.302")';
COMMENT ON COLUMN virtio_win_releases.is_stable IS 'True for the single version the upstream stable-virtio/ redirect currently points at';
COMMENT ON COLUMN virtio_win_releases.checksum IS 'Empty by default: upstream publishes no ISO checksum (its CHECKSUM file covers only the RPMs). Operator-supplied when set, and passed through to the Proxmox download-url call';

CREATE INDEX IF NOT EXISTS idx_virtio_win_releases_stable
    ON virtio_win_releases (is_stable) WHERE is_stable;

-- Per-cluster download policy.
CREATE TABLE IF NOT EXISTS virtio_win_configs (
    cluster_id     UUID PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    enabled        BOOLEAN NOT NULL DEFAULT false,
    storage        TEXT NOT NULL DEFAULT '',
    node           TEXT NOT NULL DEFAULT '',
    target_version TEXT NOT NULL DEFAULT '',
    prune_enabled  BOOLEAN NOT NULL DEFAULT false,
    last_check_at  TIMESTAMPTZ,
    last_error     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN virtio_win_configs.node IS 'Node that performs the download; empty means pick any online node in the cluster. The ISO lands on the storage, not the node, but download-url is a node-scoped call';
COMMENT ON COLUMN virtio_win_configs.target_version IS 'Pinned upstream version; empty means follow whatever upstream marks stable';
COMMENT ON COLUMN virtio_win_configs.prune_enabled IS 'Opt-in. When set, versions that are neither pinned nor newest are deleted from the target storage after a successful download. Off by default: an ISO this did not download may still be in use';

CREATE TRIGGER trg_virtio_win_configs_updated_at
    BEFORE UPDATE ON virtio_win_configs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Dispatched downloads, for UPID reconciliation and history.
CREATE TABLE IF NOT EXISTS virtio_win_downloads (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    node        TEXT NOT NULL,
    storage     TEXT NOT NULL,
    version     TEXT NOT NULL,
    filename    TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    upid        TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    triggered_by TEXT NOT NULL DEFAULT 'scheduler'
                CHECK (triggered_by IN ('scheduler', 'manual')),
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);

COMMENT ON COLUMN virtio_win_downloads.upid IS 'Proxmox task UPID returned by download-url; empty only while the dispatch itself failed before Proxmox accepted the task';

CREATE INDEX IF NOT EXISTS idx_virtio_win_downloads_cluster
    ON virtio_win_downloads (cluster_id, started_at DESC);

-- The reconcile loop scans for unfinished work on every tick; keep that scan
-- off the full history.
CREATE INDEX IF NOT EXISTS idx_virtio_win_downloads_active
    ON virtio_win_downloads (status) WHERE status IN ('pending', 'running');

-- One in-flight download per (cluster, storage, version). The scheduler already
-- checks storage content before dispatching, but that check and the insert are
-- not atomic: two ticks racing (or a manual download racing a tick) would
-- otherwise queue the same 837 MiB fetch twice.
CREATE UNIQUE INDEX IF NOT EXISTS idx_virtio_win_downloads_inflight
    ON virtio_win_downloads (cluster_id, storage, version)
    WHERE status IN ('pending', 'running');
