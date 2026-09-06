-- name: UpsertPBSSnapshot :one
INSERT INTO pbs_snapshots (pbs_server_id, datastore, backup_type, backup_id, backup_time, size, verified, protected, comment, owner, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
ON CONFLICT (pbs_server_id, datastore, backup_type, backup_id, backup_time)
DO UPDATE SET
    size = EXCLUDED.size,
    verified = EXCLUDED.verified,
    protected = EXCLUDED.protected,
    comment = EXCLUDED.comment,
    owner = EXCLUDED.owner,
    last_seen_at = now()
RETURNING *;

-- name: UpsertPBSSyncJob :one
INSERT INTO pbs_sync_jobs (pbs_server_id, job_id, store, remote, remote_store, schedule, last_run_state, next_run, comment, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (pbs_server_id, job_id)
DO UPDATE SET
    store = EXCLUDED.store,
    remote = EXCLUDED.remote,
    remote_store = EXCLUDED.remote_store,
    schedule = EXCLUDED.schedule,
    last_run_state = EXCLUDED.last_run_state,
    next_run = EXCLUDED.next_run,
    comment = EXCLUDED.comment,
    last_seen_at = now()
RETURNING *;

-- name: UpsertPBSVerifyJob :one
INSERT INTO pbs_verify_jobs (pbs_server_id, job_id, store, schedule, last_run_state, comment, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (pbs_server_id, job_id)
DO UPDATE SET
    store = EXCLUDED.store,
    schedule = EXCLUDED.schedule,
    last_run_state = EXCLUDED.last_run_state,
    comment = EXCLUDED.comment,
    last_seen_at = now()
RETURNING *;

-- name: ListPBSSnapshotsByServer :many
SELECT * FROM pbs_snapshots
WHERE pbs_server_id = $1
ORDER BY backup_time DESC;

-- name: ListPBSSnapshotsByDatastore :many
SELECT * FROM pbs_snapshots
WHERE pbs_server_id = $1 AND datastore = $2
ORDER BY backup_time DESC;

-- name: ListPBSSyncJobsByServer :many
SELECT * FROM pbs_sync_jobs
WHERE pbs_server_id = $1
ORDER BY job_id;

-- name: ListPBSVerifyJobsByServer :many
SELECT * FROM pbs_verify_jobs
WHERE pbs_server_id = $1
ORDER BY job_id;

-- name: DeleteStalePBSSnapshots :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStaleVMsForNodes in vms.sql):
-- a momentary non-observation no longer churns PBS inventory rows, and the
-- cutoff is driven from now() so the app and DB clocks aren't mixed.
DELETE FROM pbs_snapshots
WHERE pbs_server_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);

-- name: DeleteStalePBSSyncJobs :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStaleVMsForNodes in vms.sql).
DELETE FROM pbs_sync_jobs
WHERE pbs_server_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);

-- name: DeleteStalePBSVerifyJobs :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStaleVMsForNodes in vms.sql).
DELETE FROM pbs_verify_jobs
WHERE pbs_server_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);

-- name: ListPBSSnapshotsByBackupID :many
SELECT * FROM pbs_snapshots
WHERE backup_id = $1
ORDER BY backup_time DESC;

-- name: GetLatestPBSDatastoreMetrics :many
SELECT DISTINCT ON (datastore)
    time, pbs_server_id, datastore, total, used, avail
FROM pbs_datastore_metrics
WHERE pbs_server_id = $1
ORDER BY datastore, time DESC;

-- name: GetPBSDatastoreMetricsHistory :many
-- Downsampled in the database, deliberately. The raw hypertable holds one row
-- per datastore per METRICS_COLLECT_INTERVAL — 10s in docker-compose.yml and
-- .env.example, so a 7-day window runs to tens of thousands of rows per
-- datastore. That is megabytes of JSON the
-- browser then parses, formats and lays out on its only thread. The caller
-- derives bucket_seconds from the timeframe so every window comes back at
-- chart resolution (~60-170 points per datastore) instead. Averaging is the
-- right reducer here: these are capacity gauges, not counters.
--
-- The GROUP BY and ORDER BY ordinals are load-bearing — do not "clarify" them
-- to `GROUP BY time`. PostgreSQL resolves an ambiguous GROUP BY name to the
-- INPUT column, so that spelling would group by the raw per-sample timestamp,
-- silently restoring one group per row and the whole 60k-row response, with no
-- error to notice. (ORDER BY resolves the other way, preferring the output
-- alias; the ordinals sidestep the asymmetry.)
SELECT
    time_bucket(make_interval(secs => @bucket_seconds::int), time)::timestamptz AS time,
    pbs_server_id,
    datastore,
    AVG(total)::BIGINT AS total,
    AVG(used)::BIGINT AS used,
    AVG(avail)::BIGINT AS avail
FROM pbs_datastore_metrics
WHERE pbs_server_id = @pbs_server_id
  AND time >= @start_time
  AND time <= @end_time
GROUP BY 1, pbs_server_id, datastore
-- datastore breaks the tie: every datastore shares a bucket key, and the
-- HashAggregate above leaves their relative order unspecified otherwise, so
-- two identical requests could return rows in different orders.
ORDER BY 1 ASC, datastore ASC;
