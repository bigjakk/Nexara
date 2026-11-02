-- UPS monitoring through Network UPS Tools (NUT), Phase 1: read-only.
--
-- Purely additive: four new tables, one hypertable with two continuous
-- aggregates, and the view/manage/delete:ups permission family. Nothing that
-- exists is altered, so an upgrade needs no operator action.
--
-- nut_servers   one row per upsd that Nexara polls (TCP 3493)
-- ups_devices   one row per UPS a server reports, keyed on NUT's own identity
-- ups_metrics   readings over time (hypertable, 30 days raw, rollups kept)
-- ups_events    power events: outages, low battery, lost contact, self-tests

CREATE TABLE IF NOT EXISTS nut_servers (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name               TEXT NOT NULL UNIQUE,
    host               TEXT NOT NULL,
    port               INTEGER NOT NULL DEFAULT 3493 CHECK (port BETWEEN 1 AND 65535),
    username           TEXT NOT NULL DEFAULT '',
    password_encrypted TEXT NOT NULL DEFAULT '',
    tls_mode           TEXT NOT NULL DEFAULT 'off' CHECK (tls_mode IN ('off', 'starttls')),
    verify_tls         BOOLEAN NOT NULL DEFAULT TRUE,
    tls_fingerprint    TEXT NOT NULL DEFAULT '',
    enabled            BOOLEAN NOT NULL DEFAULT TRUE,
    nut_version        TEXT NOT NULL DEFAULT '',
    protocol_version   TEXT NOT NULL DEFAULT '',
    last_poll_at       TIMESTAMPTZ,
    last_poll_error    TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN nut_servers.username IS 'upsd account, if any. Reads are anonymous on most servers; a credential is only needed where upsd demands one';
COMMENT ON COLUMN nut_servers.tls_fingerprint IS 'SHA-256 of the leaf certificate presented after STARTTLS, pinned when set. Empty with verify_tls means verify against the system CA pool';
COMMENT ON COLUMN nut_servers.last_poll_at IS 'Last fully successful poll. A failing poll leaves it alone, so the UI never shows a stale server as just polled';
COMMENT ON COLUMN nut_servers.last_poll_error IS 'Why the last poll failed, empty when it succeeded';

CREATE TRIGGER trg_nut_servers_updated_at
    BEFORE UPDATE ON nut_servers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS ups_devices (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id        UUID NOT NULL REFERENCES nut_servers(id) ON DELETE CASCADE,
    ups_name         TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    manufacturer     TEXT NOT NULL DEFAULT '',
    model            TEXT NOT NULL DEFAULT '',
    serial           TEXT NOT NULL DEFAULT '',
    firmware         TEXT NOT NULL DEFAULT '',
    driver           TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL DEFAULT 'unknown'
                     CHECK (state IN ('online', 'on_battery', 'low_battery', 'off', 'unknown')),
    reachability     TEXT NOT NULL DEFAULT 'unknown'
                     CHECK (reachability IN ('ok', 'stale', 'driver_down', 'unreachable', 'unknown')),
    vars             JSONB NOT NULL DEFAULT '{}'::jsonb,
    var_descriptions JSONB NOT NULL DEFAULT '{}'::jsonb,
    present          BOOLEAN NOT NULL DEFAULT TRUE,
    last_reading_at  TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (server_id, ups_name)
);

COMMENT ON COLUMN ups_devices.ups_name IS 'NUT''s own name for the UPS, from LIST UPS. With server_id it is the identity, so a device keeps its id and history across restarts';
COMMENT ON COLUMN ups_devices.status IS 'ups.status as upsd sent it, for example "OB DISCHRG"';
COMMENT ON COLUMN ups_devices.state IS 'Derived from status the way upsmon reads it. Left at the last known value while reachability is not ok, never reset to online';
COMMENT ON COLUMN ups_devices.reachability IS 'Whether the last poll could read the UPS: ok, stale (DATA-STALE), driver_down (DRIVER-NOT-CONNECTED) or unreachable (the server did not answer)';
COMMENT ON COLUMN ups_devices.vars IS 'The latest LIST VAR reply, capped by the client';
COMMENT ON COLUMN ups_devices.present IS 'False once LIST UPS stops naming the device. Kept rather than deleted so its history survives a ups.conf edit';

CREATE TRIGGER trg_ups_devices_updated_at
    BEFORE UPDATE ON ups_devices
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- No foreign key to ups_devices, like node_pressure: deleting a server
-- deletes its devices' readings explicitly, in the same transaction.
CREATE TABLE IF NOT EXISTS ups_metrics (
    time            TIMESTAMPTZ NOT NULL,
    ups_id          UUID NOT NULL,
    battery_charge  DOUBLE PRECISION,
    battery_runtime DOUBLE PRECISION,
    battery_voltage DOUBLE PRECISION,
    ups_load        DOUBLE PRECISION,
    realpower       DOUBLE PRECISION,
    input_voltage   DOUBLE PRECISION,
    output_voltage  DOUBLE PRECISION,
    input_frequency DOUBLE PRECISION,
    temperature     DOUBLE PRECISION,
    on_battery      BOOLEAN NOT NULL
);

COMMENT ON COLUMN ups_metrics.battery_runtime IS 'Seconds, as battery.runtime reports it';
COMMENT ON COLUMN ups_metrics.realpower IS 'Watts from ups.realpower. NULL when the UPS does not report it; the API estimates from load x nominal and says so';

SELECT create_hypertable('ups_metrics', by_range('time'), if_not_exists => TRUE);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ups_metrics_ups_id_time ON ups_metrics (ups_id, time);
-- Every policy's first run waits one interval. The tables start empty, so
-- running at once gains nothing, and a job already running when a down
-- migration drops its aggregate deadlocks with it (each wants a lock the other
-- holds): the chain test's down follows its up within a second.
SELECT add_retention_policy('ups_metrics', INTERVAL '30 days',
    initial_start => now() + INTERVAL '1 day', if_not_exists => TRUE);

-- Rollups keep the conservative side of each bucket: the lowest charge and
-- runtime, the highest load, the input voltage's low and high (sags and the
-- surges a TRIM corrects), and whether the UPS was on battery at any point;
-- the average load too, for energy use later. No retention, like the other
-- aggregates, so a year of trend survives the 30-day raw window. A column
-- added later could only be back-filled from that window, so they are all
-- here from the start.
CREATE MATERIALIZED VIEW IF NOT EXISTS ups_metrics_5m
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('5 minutes', time) AS bucket,
    ups_id,
    MIN(battery_charge)  AS charge_min,
    MIN(battery_runtime) AS runtime_min,
    AVG(battery_voltage) AS battery_voltage_avg,
    MAX(ups_load)        AS load_max,
    AVG(ups_load)        AS load_avg,
    AVG(realpower)       AS realpower_avg,
    MIN(input_voltage)   AS input_voltage_min,
    MAX(input_voltage)   AS input_voltage_max,
    AVG(output_voltage)  AS output_voltage_avg,
    AVG(input_frequency) AS input_frequency_avg,
    MAX(temperature)     AS temperature_max,
    BOOL_OR(on_battery)  AS on_battery
FROM ups_metrics
GROUP BY bucket, ups_id
WITH NO DATA;

SELECT add_continuous_aggregate_policy('ups_metrics_5m',
    start_offset      => INTERVAL '30 minutes',
    end_offset        => INTERVAL '1 minute',
    schedule_interval => INTERVAL '5 minutes',
    initial_start     => now() + INTERVAL '5 minutes',
    if_not_exists     => TRUE
);

CREATE MATERIALIZED VIEW IF NOT EXISTS ups_metrics_1h
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time) AS bucket,
    ups_id,
    MIN(battery_charge)  AS charge_min,
    MIN(battery_runtime) AS runtime_min,
    AVG(battery_voltage) AS battery_voltage_avg,
    MAX(ups_load)        AS load_max,
    AVG(ups_load)        AS load_avg,
    AVG(realpower)       AS realpower_avg,
    MIN(input_voltage)   AS input_voltage_min,
    MAX(input_voltage)   AS input_voltage_max,
    AVG(output_voltage)  AS output_voltage_avg,
    AVG(input_frequency) AS input_frequency_avg,
    MAX(temperature)     AS temperature_max,
    BOOL_OR(on_battery)  AS on_battery
FROM ups_metrics
GROUP BY bucket, ups_id
WITH NO DATA;

SELECT add_continuous_aggregate_policy('ups_metrics_1h',
    start_offset      => INTERVAL '3 hours',
    end_offset        => INTERVAL '1 hour',
    schedule_interval => INTERVAL '1 hour',
    initial_start     => now() + INTERVAL '1 hour',
    if_not_exists     => TRUE
);

CREATE TABLE IF NOT EXISTS ups_events (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ups_id       UUID NOT NULL REFERENCES ups_devices(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN (
                     'on_battery', 'low_battery', 'overload', 'bypass', 'voltage_correction',
                     'replace_battery', 'unreachable', 'stale', 'self_test')),
    started_at   TIMESTAMPTZ NOT NULL,
    ended_at     TIMESTAMPTZ,
    end_observed BOOLEAN NOT NULL DEFAULT TRUE,
    details      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN ups_events.ended_at IS 'NULL while the episode is open. self_test is a point event, started_at = ended_at';
COMMENT ON COLUMN ups_events.end_observed IS 'False when the episode was closed after a gap in contact: the condition had cleared by the next reading, but when is unknown';

CREATE UNIQUE INDEX IF NOT EXISTS idx_ups_events_one_open ON ups_events (ups_id, kind) WHERE ended_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_ups_events_ups_started ON ups_events (ups_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_ups_events_started ON ups_events (started_at DESC, id DESC);

-- Permissions. No execute:ups yet: commands arrive in Phase 2 with their own
-- migration, so an upgrade that stops at this release cannot grant a
-- capability the code does not implement.
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view',   'ups', 'View UPS devices, their readings and power events, and the NUT servers that report them'),
    (gen_random_uuid(), 'manage', 'ups', 'Add and change the NUT servers Nexara reads UPS data from'),
    (gen_random_uuid(), 'delete', 'ups', 'Remove NUT servers and the UPS history collected from them')
ON CONFLICT (action, resource) DO NOTHING;

-- Built-in Admin and Operator get all three, as for Veeam (000087).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.role_id, p.id
FROM permissions p
CROSS JOIN (VALUES ('a0000000-0000-0000-0000-000000000001'::uuid),
                   ('a0000000-0000-0000-0000-000000000002'::uuid)) AS r(role_id)
WHERE p.resource = 'ups'
ON CONFLICT DO NOTHING;

-- Built-in Viewer gets read-only visibility.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions
WHERE resource = 'ups' AND action = 'view'
ON CONFLICT DO NOTHING;

-- Custom roles mirror their PBS grants action for action, for the reason
-- 000087 gives for Veeam: PBS is the other registry where a role stores a
-- secret against a remote server and causes an outbound connection.
INSERT INTO role_permissions (role_id, permission_id)
SELECT DISTINCT rp.role_id, up.id
FROM permissions up
JOIN permissions pp ON pp.action = up.action AND pp.resource = 'pbs'
JOIN role_permissions rp ON rp.permission_id = pp.id
WHERE up.resource = 'ups'
ON CONFLICT DO NOTHING;
