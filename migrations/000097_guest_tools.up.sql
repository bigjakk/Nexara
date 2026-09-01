-- 000097_guest_tools.up.sql
-- Windows guest tools (virtio drivers + QEMU guest agent) version tracking and
-- staged updates, the in-guest half of the feature migration 000096 started.
--
-- Three tables, split by who owns the row rather than by convenience:
--   guest_tools_configs  — per-cluster policy, the operator's standing intent.
--   guest_tools_policies — per-guest override and exclusion, also the operator's.
--   guest_tools_state    — observed reality plus the staging state machine,
--                          owned entirely by the reconcile loop.
-- Keeping intent and observation apart is what lets the reconciler rewrite
-- state freely without ever touching a decision a human made.
--
-- Both per-guest tables key on (cluster_id, vmid), never vms.id. The collector
-- deletes and re-inserts VM rows with a fresh UUID, and migrations 000068 and
-- 000069 exist purely to repair damage from tables that keyed on it — in one
-- case a cascade silently deleted a user's alert rules. Cleanup rides
-- ON DELETE CASCADE from clusters; read paths LEFT JOIN vms.
--
-- On version formats, because two are unavoidably in play: the catalog stores
-- upstream's directory version ("0.1.302-1") while Windows reports the
-- installer's DisplayVersion ("0.1.285"), which matches the ISO-filename form.
-- installed_version holds what the guest said; comparisons normalise to the
-- suffix-less form. Verified against a live Server 2022 guest.
--
-- Additive (new tables only) — safe for in-place upgrade, no operator action.

CREATE TABLE IF NOT EXISTS guest_tools_configs (
    cluster_id      UUID PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    mode            TEXT NOT NULL DEFAULT 'disabled'
                    CHECK (mode IN ('disabled', 'report', 'staged')),
    target_version  TEXT NOT NULL DEFAULT '',
    snapshot_before BOOLEAN NOT NULL DEFAULT false,
    max_concurrent  INT NOT NULL DEFAULT 5 CHECK (max_concurrent > 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN guest_tools_configs.mode IS 'disabled = nothing; report = detect installed versions only, no writes to any guest; staged = also stage updates for guests that are behind';
COMMENT ON COLUMN guest_tools_configs.target_version IS 'Pinned upstream version; empty means follow the cluster''s virtio-win ISO target, which in turn may follow upstream stable';
COMMENT ON COLUMN guest_tools_configs.snapshot_before IS 'Take a snapshot before staging an update. The full guest-tools bundle replaces storage and network drivers, and a bad viostor can leave a guest unbootable — this is the rollback';
COMMENT ON COLUMN guest_tools_configs.max_concurrent IS 'Cap on guests staged per pass. Swapping boot-disk drivers across a whole fleet at once turns one bad release into an outage';

CREATE TRIGGER trg_guest_tools_configs_updated_at
    BEFORE UPDATE ON guest_tools_configs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS guest_tools_policies (
    cluster_id     UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    vmid           INT NOT NULL,
    excluded       BOOLEAN NOT NULL DEFAULT false,
    target_version TEXT NOT NULL DEFAULT '',
    note           TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, vmid)
);

COMMENT ON COLUMN guest_tools_policies.excluded IS 'Never stage an update for this guest. Excluded guests stay visible in the fleet view rather than being filtered out — an exclusion nobody can see is one nobody can audit';
COMMENT ON COLUMN guest_tools_policies.note IS 'Free text for why this guest is excluded or pinned, so the reason outlives the person who set it';

CREATE TRIGGER trg_guest_tools_policies_updated_at
    BEFORE UPDATE ON guest_tools_policies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS guest_tools_state (
    cluster_id        UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    vmid              INT NOT NULL,
    installed_version TEXT NOT NULL DEFAULT '',
    agent_version     TEXT NOT NULL DEFAULT '',
    agent_running     BOOLEAN NOT NULL DEFAULT false,
    detected_at       TIMESTAMPTZ,
    stage             TEXT NOT NULL DEFAULT 'idle'
                      CHECK (stage IN ('idle', 'staging', 'staged', 'running', 'succeeded', 'failed')),
    staged_version    TEXT NOT NULL DEFAULT '',
    staged_at         TIMESTAMPTZ,
    prior_cdrom_key   TEXT NOT NULL DEFAULT '',
    prior_cdrom_value TEXT NOT NULL DEFAULT '',
    last_uptime       BIGINT NOT NULL DEFAULT 0,
    last_error        TEXT NOT NULL DEFAULT '',
    last_result_at    TIMESTAMPTZ,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, vmid)
);

COMMENT ON COLUMN guest_tools_state.installed_version IS 'DisplayVersion reported by the guest for the virtio-win installer, e.g. "0.1.285" — the ISO-filename form, without upstream''s release suffix';
COMMENT ON COLUMN guest_tools_state.stage IS 'idle -> staging -> staged -> running -> succeeded|failed. staged means the scheduled task is registered in the guest and will fire at next boot; running means it was also started on demand';
COMMENT ON COLUMN guest_tools_state.prior_cdrom_key IS 'CD-ROM device the ISO was attached to, and its previous value, so the guest''s original media is restored once the update finishes. Empty when no drive had to be borrowed';
COMMENT ON COLUMN guest_tools_state.last_uptime IS 'Guest uptime at the last poll. A drop means the guest rebooted, which is how a staged (boot-triggered) install is noticed without anything in the guest reporting in';

CREATE INDEX IF NOT EXISTS idx_guest_tools_state_active
    ON guest_tools_state (cluster_id)
    WHERE stage IN ('staging', 'staged', 'running');

CREATE TRIGGER trg_guest_tools_state_updated_at
    BEFORE UPDATE ON guest_tools_state
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
