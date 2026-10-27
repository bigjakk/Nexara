-- Storage-replication job status, collected per cluster from /cluster/replication
-- so failing jobs can surface in the health indicator.

--
-- schedule, rate, disable and comment are the job's configuration as listed;
-- the triggers on this table (migrations/000125) record a change to one of them,
-- a job appearing, and a job going. Run state — fail_count, error, last_seen_at —
-- changes on a healthy job every pass and is not watched.
-- name: UpsertReplicationJob :exec
INSERT INTO replication_jobs (cluster_id, job_id, guest, node, target, fail_count, error, schedule, rate, disable, comment, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, sqlc.arg('schedule')::text, sqlc.arg('rate')::text, sqlc.arg('disable')::int, sqlc.arg('comment')::text, now())
ON CONFLICT (cluster_id, job_id) DO UPDATE SET
    guest = EXCLUDED.guest,
    node = EXCLUDED.node,
    target = EXCLUDED.target,
    fail_count = EXCLUDED.fail_count,
    error = EXCLUDED.error,
    schedule = EXCLUDED.schedule,
    rate = EXCLUDED.rate,
    disable = EXCLUDED.disable,
    comment = EXCLUDED.comment,
    last_seen_at = now();

-- DeleteStaleReplicationJobs prunes jobs no longer reported by Proxmox (their
-- last_seen_at falls behind once they stop being upserted each sync).
-- name: DeleteStaleReplicationJobs :exec
DELETE FROM replication_jobs
WHERE cluster_id = $1 AND last_seen_at < now() - interval '15 minutes';
