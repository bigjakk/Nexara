-- name: UpsertNode :one
INSERT INTO nodes (cluster_id, name, status, cpu_count, mem_total, disk_total, pve_version, ssl_fingerprint, uptime, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (cluster_id, name) DO UPDATE SET
    status = EXCLUDED.status,
    cpu_count = EXCLUDED.cpu_count,
    mem_total = EXCLUDED.mem_total,
    disk_total = EXCLUDED.disk_total,
    pve_version = EXCLUDED.pve_version,
    ssl_fingerprint = EXCLUDED.ssl_fingerprint,
    uptime = EXCLUDED.uptime,
    last_seen_at = now()
RETURNING *;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = $1;

-- name: GetNodeByClusterAndName :one
SELECT * FROM nodes WHERE cluster_id = $1 AND name = $2;

-- name: ListNodesByCluster :many
SELECT * FROM nodes WHERE cluster_id = $1 ORDER BY name;
