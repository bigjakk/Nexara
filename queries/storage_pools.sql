-- name: UpsertStoragePool :one
INSERT INTO storage_pools (cluster_id, node_id, storage, type, content, active, enabled, shared, total, used, avail, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
ON CONFLICT (cluster_id, node_id, storage) DO UPDATE SET
    type = EXCLUDED.type,
    content = EXCLUDED.content,
    active = EXCLUDED.active,
    enabled = EXCLUDED.enabled,
    shared = EXCLUDED.shared,
    total = EXCLUDED.total,
    used = EXCLUDED.used,
    avail = EXCLUDED.avail,
    last_seen_at = now()
RETURNING *, (xmax = 0) AS inserted;

-- name: GetStoragePool :one
SELECT * FROM storage_pools WHERE id = $1;

-- name: ListStoragePoolsByNode :many
SELECT * FROM storage_pools WHERE node_id = $1 ORDER BY storage;

-- name: ListStoragePoolsByCluster :many
SELECT * FROM storage_pools WHERE cluster_id = $1 ORDER BY storage;

-- name: DeleteStoragePool :exec
DELETE FROM storage_pools WHERE id = $1;

-- name: DeleteStoragePoolsByName :exec
DELETE FROM storage_pools WHERE cluster_id = $1 AND storage = $2;

-- name: DeleteStaleStoragePools :execrows
-- Grace-windowed, DB-clock prune (see DeleteStaleVMsForNodes in vms.sql): only
-- removes pools unseen for longer than the grace window, with the cutoff driven
-- from now() so the app and DB clocks aren't mixed. Avoids churning pool rows —
-- and the spurious inventory-change events that churn emits — on a momentary
-- non-observation.
DELETE FROM storage_pools
WHERE cluster_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);
