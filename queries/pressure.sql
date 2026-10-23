-- Pressure stall information (PSI): guests' in vm_metrics, written with the
-- other metrics each cycle; nodes' in node_pressure, copied from each node's
-- RRD by the collector's pressure pass.

-- name: GetNewestNodePressureTime :one
-- The newest step stored for the node, for the pressure pass to resume after.
-- ORDER BY ... LIMIT 1 rather than max(time): sqlc types an aggregate NOT
-- NULL, and a node with no steps yet would scan a NULL into a time.Time
-- instead of answering no rows.
SELECT time FROM node_pressure
WHERE node_id = $1
ORDER BY time DESC
LIMIT 1;

-- name: InsertNodePressure :exec
-- One RRD step. A step already stored is left as it is: the pass rereads the
-- last hour each time, and a completed RRD step does not change.
INSERT INTO node_pressure (time, node_id, cpu_some, io_some, io_full, mem_some, mem_full)
VALUES (@time, @node_id, sqlc.narg(cpu_some), sqlc.narg(io_some), sqlc.narg(io_full),
        sqlc.narg(mem_some), sqlc.narg(mem_full))
ON CONFLICT (node_id, time) DO NOTHING;

-- name: GetNodePressure :many
-- A node's pressure from @since in buckets of @bucket_seconds, the peak of
-- each series per bucket: a stall is a short spike an average would flatten.
-- The peaks come through a LEFT JOIN to a CTE because that is how sqlc types
-- an aggregate as nullable (see ListVeeamOrphanedObjects): a bucket where a
-- series has no value scans as NULL instead of failing the request.
WITH s AS (
    SELECT time_bucket(make_interval(secs => @bucket_seconds::int), time) AS bucket,
           cpu_some, io_some, io_full, mem_some, mem_full
    FROM node_pressure
    WHERE node_id = @node_id AND time >= @since
),
buckets AS (
    SELECT DISTINCT bucket FROM s
),
peaks AS (
    SELECT bucket,
           max(cpu_some)::real AS cpu_some,
           max(io_some)::real  AS io_some,
           max(io_full)::real  AS io_full,
           max(mem_some)::real AS mem_some,
           max(mem_full)::real AS mem_full
    FROM s
    GROUP BY bucket
)
SELECT b.bucket::timestamptz AS bucket,
       p.cpu_some, p.io_some, p.io_full, p.mem_some, p.mem_full
FROM buckets b
LEFT JOIN peaks p ON p.bucket = b.bucket
ORDER BY 1;

-- name: GetVMPressure :many
-- A guest's pressure from @since in buckets of @bucket_seconds, the peak of
-- each series per bucket, shaped as GetNodePressure is and for the same
-- reason. A sample taken while the guest was stopped has no pressure, so a
-- bucket of those reads NULL throughout.
WITH s AS (
    SELECT time_bucket(make_interval(secs => @bucket_seconds::int), time) AS bucket,
           pressure_cpu_some, pressure_cpu_full, pressure_io_some,
           pressure_io_full, pressure_mem_some, pressure_mem_full
    FROM vm_metrics
    WHERE vm_id = @vm_id AND time >= @since
),
buckets AS (
    SELECT DISTINCT bucket FROM s
),
peaks AS (
    SELECT bucket,
           max(pressure_cpu_some)::real AS cpu_some,
           max(pressure_cpu_full)::real AS cpu_full,
           max(pressure_io_some)::real  AS io_some,
           max(pressure_io_full)::real  AS io_full,
           max(pressure_mem_some)::real AS mem_some,
           max(pressure_mem_full)::real AS mem_full
    FROM s
    GROUP BY bucket
)
SELECT b.bucket::timestamptz AS bucket,
       p.cpu_some, p.cpu_full, p.io_some, p.io_full, p.mem_some, p.mem_full
FROM buckets b
LEFT JOIN peaks p ON p.bucket = b.bucket
ORDER BY 1;
