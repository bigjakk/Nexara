-- Storage-replication job status, collected per cluster from /cluster/replication
-- so failing jobs can surface in the health indicator.

-- name: UpsertReplicationJob :exec
INSERT INTO replication_jobs (cluster_id, job_id, guest, node, target, fail_count, error, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (cluster_id, job_id) DO UPDATE SET
    guest = EXCLUDED.guest,
    node = EXCLUDED.node,
    target = EXCLUDED.target,
    fail_count = EXCLUDED.fail_count,
    error = EXCLUDED.error,
    last_seen_at = now();

-- DeleteStaleReplicationJobs prunes jobs no longer reported by Proxmox (their
-- last_seen_at falls behind once they stop being upserted each sync).
-- name: DeleteStaleReplicationJobs :exec
DELETE FROM replication_jobs
WHERE cluster_id = $1 AND last_seen_at < now() - interval '15 minutes';
