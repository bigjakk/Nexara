-- Health-aggregator queries: each returns ONLY problem rows across all clusters,
-- so the clusters list can attach a generic issues[] without per-cluster calls.

-- name: ListNodeHealthProblems :many
-- Offline nodes and HA-fenced nodes.
SELECT cluster_id, name, status, ha_state
FROM nodes
WHERE status = 'offline' OR ha_state = 'fence';

-- name: ListFailedDisks :many
-- Physical disks whose SMART overall-health assessment reports failure. A
-- deny-list (LIKE 'FAIL%') rather than an allow-list, so an unexpected benign
-- string (controller-specific, OLD-AGE, …) can't raise a false hardware alert.
SELECT d.cluster_id, n.name AS node_name, d.dev_path, d.model, d.health
FROM node_disks d
JOIN nodes n ON n.id = d.node_id
WHERE upper(d.health) LIKE 'FAIL%';

-- name: ListInactiveStorage :many
-- Storage that is enabled but not currently active (unreachable). Shared pools
-- appear once per node, so de-duplicate by (cluster_id, storage).
SELECT DISTINCT cluster_id, storage
FROM storage_pools
WHERE enabled AND NOT active;

-- name: ListStorageNearFull :many
-- Active storage at or above 85% usage. De-duplicate shared pools.
SELECT DISTINCT ON (cluster_id, storage) cluster_id, storage, total, used
FROM storage_pools
WHERE enabled AND active AND total > 0 AND (used::float8 / total::float8) >= 0.85
ORDER BY cluster_id, storage, used DESC;

-- name: ListRecentFailedTasksByType :many
-- Failed tasks in the last 24h, grouped by type so we surface a count, not spam.
SELECT cluster_id, task_type, count(*)::int AS cnt
FROM task_history
WHERE status = 'failed' AND started_at > now() - interval '24 hours'
GROUP BY cluster_id, task_type
ORDER BY cluster_id, task_type;

-- name: ListHAErrorGuests :many
-- Guests whose HA resource state is "error" (needs manual intervention).
SELECT cluster_id, name
FROM vms
WHERE ha_state = 'error';

-- name: ListNonQuorateClusters :many
-- Active clusters that have lost quorum.
SELECT id FROM clusters WHERE is_active AND NOT quorate;

-- name: ListRootfsFullNodes :many
-- Nodes whose root filesystem is at or above 85% usage.
SELECT cluster_id, name, rootfs_used, disk_total
FROM nodes
WHERE disk_total > 0 AND (rootfs_used::float8 / disk_total::float8) >= 0.85;

-- name: ListIOErrorGuests :many
-- Guests paused by a storage I/O error (Proxmox signals this via the guest lock).
SELECT cluster_id, name
FROM vms
WHERE lock_state = 'io-error';

-- name: ListFailedReplication :many
-- Replication jobs currently in an error state. Proxmox resets fail_count to 0
-- on the next successful run, so gating on fail_count (not the possibly-stale
-- error string) matches "in error state" as the PVE GUI shows it.
SELECT cluster_id, guest, node, target, fail_count, error
FROM replication_jobs
WHERE fail_count > 0;
