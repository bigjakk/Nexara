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
DELETE FROM pbs_snapshots
WHERE pbs_server_id = $1 AND last_seen_at < $2;

-- name: DeleteStalePBSSyncJobs :exec
DELETE FROM pbs_sync_jobs
WHERE pbs_server_id = $1 AND last_seen_at < $2;

-- name: DeleteStalePBSVerifyJobs :exec
DELETE FROM pbs_verify_jobs
WHERE pbs_server_id = $1 AND last_seen_at < $2;

-- name: GetLatestPBSDatastoreMetrics :many
SELECT DISTINCT ON (datastore)
    time, pbs_server_id, datastore, total, used, avail
FROM pbs_datastore_metrics
WHERE pbs_server_id = $1
ORDER BY datastore, time DESC;

-- name: GetPBSDatastoreMetricsHistory :many
SELECT time, pbs_server_id, datastore, total, used, avail
FROM pbs_datastore_metrics
WHERE pbs_server_id = $1 AND time >= $2 AND time <= $3
ORDER BY time ASC;
