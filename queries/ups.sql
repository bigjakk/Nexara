-- UPS devices, their readings and their power events (000131).

-- name: UpsertUpsDevice :one
-- Keyed on (server_id, ups_name), NUT's own identity, so a device keeps its id
-- and history across polls, restarts and a ups.conf edit that drops and then
-- restores it.
INSERT INTO ups_devices (server_id, ups_name, description)
VALUES ($1, $2, $3)
ON CONFLICT (server_id, ups_name) DO UPDATE
SET description = EXCLUDED.description,
    present = TRUE
RETURNING *;

-- name: MarkUpsDevicesAbsent :exec
-- Devices LIST UPS no longer names. Kept, not deleted, so their history stays.
-- A nil Go slice reaches SQL as NULL, which ANY would match against nothing;
-- COALESCE makes it the empty list it means, so every device is marked absent.
UPDATE ups_devices
SET present = FALSE
WHERE server_id = @server_id
  AND present
  AND NOT (ups_name = ANY(COALESCE(@present_names::text[], '{}')));

-- name: ListUpsDevicesByServer :many
SELECT * FROM ups_devices WHERE server_id = $1 AND present ORDER BY ups_name;

-- name: RecordUpsReading :exec
UPDATE ups_devices
SET status = @status,
    state = @state,
    reachability = 'ok',
    vars = @vars,
    manufacturer = @manufacturer,
    model = @model,
    serial = @serial,
    firmware = @firmware,
    driver = @driver,
    last_reading_at = @read_at
WHERE id = @id;

-- name: SetUpsReachability :exec
-- state is left as it was. A UPS Nexara cannot read is neither on mains nor
-- on battery as far as Nexara knows, and the UI and the alert engine read the
-- two columns together.
UPDATE ups_devices SET reachability = $2 WHERE id = $1;

-- name: MergeUpsVarDescriptions :exec
UPDATE ups_devices
SET var_descriptions = var_descriptions || @descriptions::jsonb
WHERE id = @id;

-- name: InsertUpsMetric :exec
INSERT INTO ups_metrics (
    time, ups_id, battery_charge, battery_runtime, battery_voltage, ups_load,
    realpower, input_voltage, output_voltage, input_frequency, temperature, on_battery
)
VALUES (
    @time, @ups_id, sqlc.narg(battery_charge), sqlc.narg(battery_runtime),
    sqlc.narg(battery_voltage), sqlc.narg(ups_load), sqlc.narg(realpower),
    sqlc.narg(input_voltage), sqlc.narg(output_voltage), sqlc.narg(input_frequency),
    sqlc.narg(temperature), @on_battery
)
ON CONFLICT (ups_id, time) DO NOTHING;

-- name: ListUpsDevices :many
-- Every present device with its server, for the Power page, the health pill
-- and the alert engine.
SELECT d.*,
       s.name AS server_name,
       s.host AS server_host,
       s.port AS server_port,
       s.enabled AS server_enabled,
       s.host_cluster_id AS server_host_cluster_id,
       s.host_node_name AS server_host_node_name
FROM ups_devices d
JOIN nut_servers s ON s.id = d.server_id
WHERE d.present
ORDER BY s.name, d.ups_name;

-- name: GetUpsDevice :one
-- One device with its server. Absent devices are still readable by id, so a
-- link to one keeps working after LIST UPS drops it.
SELECT d.*,
       s.name AS server_name,
       s.host AS server_host,
       s.port AS server_port,
       s.enabled AS server_enabled,
       s.host_cluster_id AS server_host_cluster_id,
       s.host_node_name AS server_host_node_name
FROM ups_devices d
JOIN nut_servers s ON s.id = d.server_id
WHERE d.id = $1;

-- name: GetUpsMetricSeries :many
-- One UPS's readings from @since in buckets of @bucket_seconds, read from the
-- raw rows (@source 'raw'), the 5-minute rollup ('5m') or the hourly one
-- ('1h'); only the named branch returns rows. Each bucket keeps the
-- conservative side, as the rollups do: the lowest charge and runtime, the
-- input voltage's low and high, the highest load and temperature, and whether
-- the UPS was on battery at any point. The values come through a LEFT JOIN to
-- a CTE so that sqlc types them nullable (see GetNodePressure).
WITH src AS (
    SELECT time AS t, battery_charge AS charge, battery_runtime AS runtime, battery_voltage,
           ups_load AS load_pct, realpower, input_voltage AS input_lo, input_voltage AS input_hi,
           output_voltage, input_frequency, temperature, on_battery
    FROM ups_metrics
    WHERE @source::text = 'raw' AND ups_id = @ups_id::uuid AND time >= @since::timestamptz
    UNION ALL
    SELECT bucket, charge_min, runtime_min, battery_voltage_avg, load_max, realpower_avg,
           input_voltage_min, input_voltage_max, output_voltage_avg, input_frequency_avg,
           temperature_max, on_battery
    FROM ups_metrics_5m
    WHERE @source::text = '5m' AND ups_id = @ups_id::uuid AND bucket >= @since::timestamptz
    UNION ALL
    SELECT bucket, charge_min, runtime_min, battery_voltage_avg, load_max, realpower_avg,
           input_voltage_min, input_voltage_max, output_voltage_avg, input_frequency_avg,
           temperature_max, on_battery
    FROM ups_metrics_1h
    WHERE @source::text = '1h' AND ups_id = @ups_id::uuid AND bucket >= @since::timestamptz
),
s AS (
    SELECT time_bucket(make_interval(secs => @bucket_seconds::int), t) AS bucket,
           charge, runtime, battery_voltage, load_pct, realpower, input_lo, input_hi,
           output_voltage, input_frequency, temperature, on_battery
    FROM src
),
buckets AS (
    SELECT DISTINCT bucket FROM s
),
agg AS (
    SELECT bucket,
           min(charge)::double precision          AS charge_min,
           min(runtime)::double precision         AS runtime_min,
           avg(battery_voltage)::double precision AS battery_voltage,
           max(load_pct)::double precision        AS load_max,
           avg(realpower)::double precision       AS realpower,
           min(input_lo)::double precision        AS input_voltage_min,
           max(input_hi)::double precision        AS input_voltage_max,
           avg(output_voltage)::double precision  AS output_voltage,
           avg(input_frequency)::double precision AS input_frequency,
           max(temperature)::double precision     AS temperature_max,
           bool_or(on_battery)  AS on_battery
    FROM s
    GROUP BY bucket
)
SELECT b.bucket::timestamptz AS bucket,
       a.charge_min, a.runtime_min, a.battery_voltage, a.load_max, a.realpower,
       a.input_voltage_min, a.input_voltage_max, a.output_voltage, a.input_frequency, a.temperature_max,
       a.on_battery
FROM buckets b
LEFT JOIN agg a ON a.bucket = b.bucket
ORDER BY 1;

-- name: ListOpenUpsEventsByServer :many
-- Open episodes of one server's devices, for the poller to resume after a restart.
SELECT e.*
FROM ups_events e
JOIN ups_devices d ON d.id = e.ups_id
WHERE d.server_id = $1 AND e.ended_at IS NULL;

-- name: OpenUpsEvent :exec
-- At most one open episode per (UPS, kind); a second open is a no-op.
INSERT INTO ups_events (ups_id, kind, started_at, details)
VALUES (@ups_id, @kind, @started_at, @details)
ON CONFLICT (ups_id, kind) WHERE ended_at IS NULL DO NOTHING;

-- name: UpdateOpenUpsEvent :exec
UPDATE ups_events
SET details = @details
WHERE ups_id = @ups_id AND kind = @kind AND ended_at IS NULL;

-- name: CloseUpsEvent :exec
UPDATE ups_events
SET ended_at = @ended_at,
    end_observed = @end_observed,
    details = @details
WHERE ups_id = @ups_id AND kind = @kind AND ended_at IS NULL;

-- name: CloseUpsEventsOfDisabledServers :execrows
-- Nothing watches a disabled server's UPSes, so an episode it left open is
-- closed as not observed rather than left reading as ongoing, and tagged so
-- the history says polling was turned off, not that contact was lost.
UPDATE ups_events e
SET ended_at = now(), end_observed = false,
    details = e.details || '{"ended_by": "polling_off"}'::jsonb
FROM ups_devices d
JOIN nut_servers s ON s.id = d.server_id
WHERE e.ups_id = d.id AND e.ended_at IS NULL AND NOT s.enabled;

-- name: InsertUpsPointEvent :exec
-- An event with no duration, such as a self-test result.
INSERT INTO ups_events (ups_id, kind, started_at, ended_at, details)
VALUES (@ups_id, @kind, @at, @at, @details);

-- name: ListUpsEvents :many
-- Newest first, keyset-paged on (started_at, id). ups_id and kind filter when set.
-- details is aliased so the handler never reads a field named Details: in
-- package handlers that name means an audit row's, and
-- TestGuard_AuditReadPathsRedact keys on it.
SELECT e.id, e.ups_id, e.kind, e.started_at, e.ended_at, e.end_observed, e.details AS event_details,
       d.ups_name, d.description AS ups_description, s.name AS server_name
FROM ups_events e
JOIN ups_devices d ON d.id = e.ups_id
JOIN nut_servers s ON s.id = d.server_id
WHERE (sqlc.narg('ups_id')::uuid IS NULL OR e.ups_id = sqlc.narg('ups_id')::uuid)
  AND (sqlc.narg('kind')::text IS NULL OR e.kind = sqlc.narg('kind')::text)
  AND (sqlc.narg('before_started_at')::timestamptz IS NULL
       OR (e.started_at, e.id) < (sqlc.narg('before_started_at')::timestamptz, sqlc.narg('before_id')::uuid))
ORDER BY e.started_at DESC, e.id DESC
LIMIT sqlc.arg('row_limit');

-- name: ListOpenUpsEvents :many
-- Every open episode, so the device list can say "on battery since 03:12"
-- without a query per device. Served by idx_ups_events_one_open.
SELECT ups_id, kind, started_at FROM ups_events WHERE ended_at IS NULL;
