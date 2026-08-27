-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000088_veeam_inventory.up.sql
-- Phase 2 of the Veeam integration: the inventory the collector fills in.
-- Repositories (and their capacity over time), jobs, backup objects, restore
-- points and sessions.
--
-- Purely additive — five new tables, one hypertable, one continuous aggregate.
-- Nothing existing is touched.
--
-- Two things this migration deliberately does NOT add:
--
--   * Correlation columns on veeam_backup_objects (cluster_id, vmid,
--     match_method). Phase 3 owns guest correlation and should pick their
--     shape after measuring against real data, rather than inheriting columns
--     no code has exercised. Adding nullable columns later is a trivial
--     migration.
--   * Anything for scale-out repositories. ScaleOutRepositoryModel in the
--     1.3-rev2 spec is a configuration model — performanceTier/capacityTier/
--     archiveTier — with no capacity or usage fields anywhere, and the lab
--     server has zero SOBRs, so none of it could be tested. A SOBR's real
--     capacity lives in its extents, which DO appear in
--     /repositories/states and are covered below.

-- veeam_repositories — backup targets, from
-- GET /api/v1/backupInfrastructure/repositories/states.
--
-- That path is the one the API actually serves; Veeam's published reference
-- documents it as /api/v1/repositories, which 404s. It is also the only
-- listing that carries usage, which is why it is preferred over the plain
-- /repositories config listing.
CREATE TABLE IF NOT EXISTS veeam_repositories (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    veeam_server_id  UUID NOT NULL REFERENCES veeam_servers(id) ON DELETE CASCADE,
    veeam_id         UUID NOT NULL,
    name             TEXT NOT NULL,
    repo_type        TEXT NOT NULL DEFAULT '',
    host_name        TEXT NOT NULL DEFAULT '',
    path             TEXT NOT NULL DEFAULT '',
    capacity_bytes   BIGINT NOT NULL DEFAULT 0,
    free_bytes       BIGINT NOT NULL DEFAULT 0,
    used_bytes       BIGINT NOT NULL DEFAULT 0,
    is_online        BOOLEAN NOT NULL DEFAULT false,
    is_out_of_date   BOOLEAN NOT NULL DEFAULT false,
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (veeam_server_id, veeam_id)
);

COMMENT ON COLUMN veeam_repositories.capacity_bytes IS 'Converted from the API''s float capacityGB. The conversion belongs in the client so every consumer sees bytes, matching pbs_datastore_metrics';
COMMENT ON COLUMN veeam_repositories.host_name IS 'Veeam''s hostName for the repository, e.g. "Direct" for an object-store target';

CREATE INDEX IF NOT EXISTS idx_veeam_repositories_server ON veeam_repositories (veeam_server_id);

CREATE TRIGGER trg_veeam_repositories_updated_at
    BEFORE UPDATE ON veeam_repositories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- veeam_repository_metrics — capacity over time, per 000007's pbs_datastore_metrics.
--
-- Keyed on Veeam's own repository id rather than veeam_repositories.id: the
-- surrogate key is ours and could in principle be re-minted, while the Veeam
-- id is the stable identity the samples belong to. Same shape as
-- pbs_datastore_metrics, which keys on (pbs_server_id, datastore) rather than
-- a datastore row.
CREATE TABLE IF NOT EXISTS veeam_repository_metrics (
    time                TIMESTAMPTZ NOT NULL,
    veeam_server_id     UUID NOT NULL REFERENCES veeam_servers(id) ON DELETE CASCADE,
    repository_veeam_id UUID NOT NULL,
    capacity_bytes      BIGINT NOT NULL DEFAULT 0,
    free_bytes          BIGINT NOT NULL DEFAULT 0,
    used_bytes          BIGINT NOT NULL DEFAULT 0
);

SELECT create_hypertable('veeam_repository_metrics', 'time', if_not_exists => TRUE);

SELECT add_retention_policy('veeam_repository_metrics', INTERVAL '30 days', if_not_exists => TRUE);

CREATE MATERIALIZED VIEW IF NOT EXISTS veeam_repository_metrics_5m
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('5 minutes', time) AS bucket,
    veeam_server_id,
    repository_veeam_id,
    AVG(capacity_bytes)::BIGINT AS capacity_bytes,
    AVG(free_bytes)::BIGINT     AS free_bytes,
    AVG(used_bytes)::BIGINT     AS used_bytes
FROM veeam_repository_metrics
GROUP BY bucket, veeam_server_id, repository_veeam_id
WITH NO DATA;

SELECT add_continuous_aggregate_policy('veeam_repository_metrics_5m',
    start_offset      => INTERVAL '1 hour',
    end_offset        => INTERVAL '5 minutes',
    schedule_interval => INTERVAL '5 minutes',
    if_not_exists     => TRUE
);

-- veeam_jobs — from GET /api/v1/jobs/states.
--
-- NOT from /api/v1/jobs: Proxmox jobs are absent from that listing entirely on
-- 13.1, and GET /jobs/{id} rejects them with "Specify job of supported
-- platform type." Job *state* is the only view of a Proxmox job the REST API
-- offers, so this table is modelled on the state payload — there is no
-- schedule or retention detail to store because there is none to read.
CREATE TABLE IF NOT EXISTS veeam_jobs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    veeam_server_id     UUID NOT NULL REFERENCES veeam_servers(id) ON DELETE CASCADE,
    veeam_id            UUID NOT NULL,
    name                TEXT NOT NULL,
    job_type            TEXT NOT NULL DEFAULT '',
    workload            TEXT NOT NULL DEFAULT '',
    description         TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT '',
    last_result         TEXT NOT NULL DEFAULT '',
    last_run            TIMESTAMPTZ,
    next_run            TIMESTAMPTZ,
    next_run_policy     TEXT NOT NULL DEFAULT '',
    repository_veeam_id UUID,
    repository_name     TEXT NOT NULL DEFAULT '',
    objects_count       INTEGER NOT NULL DEFAULT 0,
    last_session_id     UUID,
    progress_percent    INTEGER NOT NULL DEFAULT 0,
    bottleneck          TEXT NOT NULL DEFAULT '',
    duration            TEXT NOT NULL DEFAULT '',
    processing_rate     TEXT NOT NULL DEFAULT '',
    processed_size      BIGINT NOT NULL DEFAULT 0,
    read_size           BIGINT NOT NULL DEFAULT 0,
    transferred_size    BIGINT NOT NULL DEFAULT 0,
    platform_id         UUID,
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (veeam_server_id, veeam_id)
);

COMMENT ON COLUMN veeam_jobs.platform_id IS 'DERIVED from sessions and STICKY — job states carry no platformId, sessions are the only bridge. Once set it is never cleared: session retention pruning would otherwise silently un-attribute a job and drop it out of a cluster-scoped user''s view with nothing to explain why';
COMMENT ON COLUMN veeam_jobs.duration IS 'Veeam''s own formatted duration ("00:18:27"), stored as given — it is a display value, not something to compute with';
COMMENT ON COLUMN veeam_jobs.bottleneck IS 'Veeam''s bottleneck analysis (Source/Target/Network/Proxy/NotDefined)';

CREATE INDEX IF NOT EXISTS idx_veeam_jobs_server ON veeam_jobs (veeam_server_id);
CREATE INDEX IF NOT EXISTS idx_veeam_jobs_platform ON veeam_jobs (platform_id);

CREATE TRIGGER trg_veeam_jobs_updated_at
    BEFORE UPDATE ON veeam_jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- veeam_backup_objects — from GET /api/v1/backupObjects.
--
-- One row per (guest × backup), so a guest protected by daily and weekly jobs
-- appears more than once. Any per-guest rollup must therefore aggregate;
-- a naive join reports the guest N times.
CREATE TABLE IF NOT EXISTS veeam_backup_objects (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    veeam_server_id      UUID NOT NULL REFERENCES veeam_servers(id) ON DELETE CASCADE,
    veeam_object_id      UUID NOT NULL,
    smbios_uuid          TEXT NOT NULL DEFAULT '',
    platform_id          UUID,
    name                 TEXT NOT NULL,
    object_type          TEXT NOT NULL DEFAULT '',
    backup_ref           UUID,
    restore_points_count INTEGER NOT NULL DEFAULT 0,
    size_bytes           BIGINT NOT NULL DEFAULT 0,
    last_run_failed      BOOLEAN NOT NULL DEFAULT false,
    last_seen_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (veeam_server_id, veeam_object_id)
);

COMMENT ON COLUMN veeam_backup_objects.veeam_object_id IS 'Veeam''s row id for the backup object (the payload''s "id"), NOT the guest identity';
COMMENT ON COLUMN veeam_backup_objects.smbios_uuid IS 'The payload''s "objectId", which IS the Proxmox smbios1 uuid — verified 15/18 exact matches on the lab cluster. This is what makes guest correlation deterministic in Phase 3 rather than a name match';
COMMENT ON COLUMN veeam_backup_objects.backup_ref IS 'The payload''s "backupId". Verified against the live server: it does NOT resolve into GET /api/v1/backups — a different id space despite the name. Stored for reference only; the object-to-restore-point link comes from GET /backupObjects/{id}/restorePoints, which is authoritative';
COMMENT ON COLUMN veeam_backup_objects.restore_points_count IS 'Veeam''s own count, stored for display. Deliberately NOT used to decide whether to re-fetch this object''s restore points: a job that keeps N points saturates at N and the count stops moving, so a count-gated refresh would freeze permanently on the day the ceiling was hit';

CREATE INDEX IF NOT EXISTS idx_veeam_backup_objects_server ON veeam_backup_objects (veeam_server_id);
CREATE INDEX IF NOT EXISTS idx_veeam_backup_objects_platform ON veeam_backup_objects (platform_id);
CREATE INDEX IF NOT EXISTS idx_veeam_backup_objects_smbios ON veeam_backup_objects (smbios_uuid) WHERE smbios_uuid <> '';

CREATE TRIGGER trg_veeam_backup_objects_updated_at
    BEFORE UPDATE ON veeam_backup_objects
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- veeam_restore_points — from GET /api/v1/backupObjects/{id}/restorePoints.
--
-- Per-object rather than the bulk GET /api/v1/restorePoints, because the bulk
-- listing carries no object id: linking its rows back would mean matching on
-- (platformId, name), and a rebuilt guest reuses its name. That merges the old
-- machine's restore points into the new one and destroys exactly the orphan
-- detection this integration exists to provide.
--
-- CASCADE from veeam_backup_objects is safe here in a way it would not be for
-- a guest row: the collector UPSERTs objects on (veeam_server_id,
-- veeam_object_id) and never delete-then-inserts them, so the parent id does
-- not churn. A parent that genuinely disappears should take its points with it.
CREATE TABLE IF NOT EXISTS veeam_restore_points (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    veeam_server_id  UUID NOT NULL REFERENCES veeam_servers(id) ON DELETE CASCADE,
    backup_object_id UUID NOT NULL REFERENCES veeam_backup_objects(id) ON DELETE CASCADE,
    veeam_id         UUID NOT NULL,
    name             TEXT NOT NULL DEFAULT '',
    point_type       TEXT NOT NULL DEFAULT '',
    malware_status   TEXT NOT NULL DEFAULT '',
    guest_os_family  TEXT NOT NULL DEFAULT '',
    creation_time    TIMESTAMPTZ NOT NULL,
    size_bytes       BIGINT NOT NULL DEFAULT 0,
    backup_id        UUID,
    session_id       UUID,
    backup_file_id   UUID,
    supports_flr     BOOLEAN NOT NULL DEFAULT false,
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (veeam_server_id, veeam_id)
);

COMMENT ON COLUMN veeam_restore_points.malware_status IS 'Rides on every restore point, so per-guest malware state needs no separate /malwareDetection sync';
COMMENT ON COLUMN veeam_restore_points.supports_flr IS 'Derived from allowedOperations containing StartFlrRestore. File-level restore is the only restore Proxmox supports on 13.1 — entire-VM and instant recovery do not exist for this platform';
COMMENT ON COLUMN veeam_restore_points.last_seen_at IS 'Retention prunes on THIS, never on creation_time: a restore point Veeam still holds must not vanish from Nexara just because it is old, or every RPO and coverage number derived from it becomes wrong';

CREATE INDEX IF NOT EXISTS idx_veeam_restore_points_object ON veeam_restore_points (backup_object_id, creation_time DESC);
CREATE INDEX IF NOT EXISTS idx_veeam_restore_points_seen ON veeam_restore_points (last_seen_at);

-- veeam_sessions — from GET /api/v1/sessions.
--
-- Not task_history: a Veeam session is a UUID from a foreign scheduler with no
-- node and no pid:starttime, so there is nothing to reconcile against
-- /nodes/*/tasks and handlers.TrackTask does not apply.
--
-- Proxmox backup sessions are sessionType "PlatformBackupJob" with
-- platformName "Proxmox" — there is no Proxmox value in ESessionType. The poll
-- filters server-side on typeFilter because ConfigurationResynchronize is 109
-- of every 200 rows on a real server and would otherwise consume the whole
-- page budget.
CREATE TABLE IF NOT EXISTS veeam_sessions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    veeam_server_id  UUID NOT NULL REFERENCES veeam_servers(id) ON DELETE CASCADE,
    veeam_id         UUID NOT NULL,
    job_veeam_id     UUID,
    name             TEXT NOT NULL DEFAULT '',
    session_type     TEXT NOT NULL DEFAULT '',
    platform_name    TEXT NOT NULL DEFAULT '',
    platform_id      UUID,
    state            TEXT NOT NULL DEFAULT '',
    result           TEXT NOT NULL DEFAULT '',
    result_message   TEXT NOT NULL DEFAULT '',
    is_canceled      BOOLEAN NOT NULL DEFAULT false,
    algorithm        TEXT NOT NULL DEFAULT '',
    bottleneck       TEXT NOT NULL DEFAULT '',
    duration         TEXT NOT NULL DEFAULT '',
    processing_rate  TEXT NOT NULL DEFAULT '',
    processed_size   BIGINT NOT NULL DEFAULT 0,
    read_size        BIGINT NOT NULL DEFAULT 0,
    transferred_size BIGINT NOT NULL DEFAULT 0,
    progress_percent INTEGER NOT NULL DEFAULT 0,
    creation_time    TIMESTAMPTZ NOT NULL,
    end_time         TIMESTAMPTZ,
    initiated_by     TEXT NOT NULL DEFAULT '',
    nexara_initiated BOOLEAN NOT NULL DEFAULT false,
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (veeam_server_id, veeam_id)
);

COMMENT ON COLUMN veeam_sessions.is_canceled IS 'Veeam''s own flag, which is FALSE even for a session cancelled through its API — a cancelled job is recorded as result "Failed" with an empty log and nothing distinguishing it from a real failure. Do not trust this to mean "not cancelled"';
COMMENT ON COLUMN veeam_sessions.nexara_initiated IS 'Set when Nexara itself started or stopped the job, taken from the 201 response that carries the session inline. The only way to tell an operator-requested stop from a genuine failure; unused until job control ships';
COMMENT ON COLUMN veeam_sessions.platform_id IS 'Carried directly by sessions, unlike job states. This is what makes a session cluster-scopable, and what veeam_jobs.platform_id is derived from';

CREATE INDEX IF NOT EXISTS idx_veeam_sessions_server_time ON veeam_sessions (veeam_server_id, creation_time DESC);
CREATE INDEX IF NOT EXISTS idx_veeam_sessions_job ON veeam_sessions (veeam_server_id, job_veeam_id);
CREATE INDEX IF NOT EXISTS idx_veeam_sessions_platform ON veeam_sessions (platform_id);
