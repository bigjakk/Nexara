-- 000007_pbs_metrics.up.sql
-- PBS inventory tables and datastore metrics hypertable.

-- Add tls_fingerprint to pbs_servers (needed for self-signed certs).
ALTER TABLE pbs_servers ADD COLUMN IF NOT EXISTS tls_fingerprint TEXT NOT NULL DEFAULT '';

-- pbs_datastore_metrics — TimescaleDB hypertable for datastore capacity over time.
CREATE TABLE IF NOT EXISTS pbs_datastore_metrics (
    time           TIMESTAMPTZ NOT NULL,
    pbs_server_id  UUID NOT NULL REFERENCES pbs_servers(id) ON DELETE CASCADE,
    datastore      TEXT NOT NULL,
    total          BIGINT NOT NULL DEFAULT 0,
    used           BIGINT NOT NULL DEFAULT 0,
    avail          BIGINT NOT NULL DEFAULT 0
);

SELECT create_hypertable('pbs_datastore_metrics', 'time', if_not_exists => TRUE);

-- 30-day retention policy.
SELECT add_retention_policy('pbs_datastore_metrics', INTERVAL '30 days', if_not_exists => TRUE);

-- 5-minute continuous aggregate.
CREATE MATERIALIZED VIEW IF NOT EXISTS pbs_datastore_metrics_5m
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('5 minutes', time) AS bucket,
    pbs_server_id,
    datastore,
    AVG(total)::BIGINT AS total,
    AVG(used)::BIGINT AS used,
    AVG(avail)::BIGINT AS avail
FROM pbs_datastore_metrics
GROUP BY bucket, pbs_server_id, datastore
WITH NO DATA;

SELECT add_continuous_aggregate_policy('pbs_datastore_metrics_5m',
    start_offset => INTERVAL '1 hour',
    end_offset   => INTERVAL '5 minutes',
    schedule_interval => INTERVAL '5 minutes',
    if_not_exists => TRUE
);

-- pbs_snapshots — inventory of all PBS snapshots, synced by collector.
CREATE TABLE IF NOT EXISTS pbs_snapshots (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pbs_server_id  UUID NOT NULL REFERENCES pbs_servers(id) ON DELETE CASCADE,
    datastore      TEXT NOT NULL,
    backup_type    TEXT NOT NULL,
    backup_id      TEXT NOT NULL,
    backup_time    BIGINT NOT NULL,
    size           BIGINT NOT NULL DEFAULT 0,
    verified       BOOLEAN NOT NULL DEFAULT false,
    protected      BOOLEAN NOT NULL DEFAULT false,
    comment        TEXT NOT NULL DEFAULT '',
    owner          TEXT NOT NULL DEFAULT '',
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (pbs_server_id, datastore, backup_type, backup_id, backup_time)
);

CREATE INDEX IF NOT EXISTS idx_pbs_snapshots_server_id ON pbs_snapshots (pbs_server_id);
CREATE INDEX IF NOT EXISTS idx_pbs_snapshots_datastore ON pbs_snapshots (pbs_server_id, datastore);

CREATE TRIGGER trg_pbs_snapshots_updated_at
    BEFORE UPDATE ON pbs_snapshots
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- pbs_sync_jobs — inventory of sync jobs.
CREATE TABLE IF NOT EXISTS pbs_sync_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pbs_server_id   UUID NOT NULL REFERENCES pbs_servers(id) ON DELETE CASCADE,
    job_id          TEXT NOT NULL,
    store           TEXT NOT NULL,
    remote          TEXT NOT NULL DEFAULT '',
    remote_store    TEXT NOT NULL DEFAULT '',
    schedule        TEXT NOT NULL DEFAULT '',
    last_run_state  TEXT NOT NULL DEFAULT '',
    next_run        BIGINT NOT NULL DEFAULT 0,
    comment         TEXT NOT NULL DEFAULT '',
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (pbs_server_id, job_id)
);

CREATE INDEX IF NOT EXISTS idx_pbs_sync_jobs_server_id ON pbs_sync_jobs (pbs_server_id);

CREATE TRIGGER trg_pbs_sync_jobs_updated_at
    BEFORE UPDATE ON pbs_sync_jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- pbs_verify_jobs — inventory of verify jobs.
CREATE TABLE IF NOT EXISTS pbs_verify_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pbs_server_id   UUID NOT NULL REFERENCES pbs_servers(id) ON DELETE CASCADE,
    job_id          TEXT NOT NULL,
    store           TEXT NOT NULL,
    schedule        TEXT NOT NULL DEFAULT '',
    last_run_state  TEXT NOT NULL DEFAULT '',
    comment         TEXT NOT NULL DEFAULT '',
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (pbs_server_id, job_id)
);

CREATE INDEX IF NOT EXISTS idx_pbs_verify_jobs_server_id ON pbs_verify_jobs (pbs_server_id);

CREATE TRIGGER trg_pbs_verify_jobs_updated_at
    BEFORE UPDATE ON pbs_verify_jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
