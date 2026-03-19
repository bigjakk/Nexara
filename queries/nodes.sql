-- name: UpsertNode :one
INSERT INTO nodes (cluster_id, name, status, cpu_count, mem_total, disk_total, pve_version, ssl_fingerprint, uptime,
                   cpu_model, cpu_cores, cpu_sockets, cpu_threads, cpu_mhz, kernel_version,
                   swap_total, swap_used, swap_free, dns_servers, dns_search, timezone,
                   subscription_status, subscription_level, load_avg, io_wait, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
        $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, now())
ON CONFLICT (cluster_id, name) DO UPDATE SET
    status = EXCLUDED.status,
    cpu_count = EXCLUDED.cpu_count,
    mem_total = EXCLUDED.mem_total,
    disk_total = EXCLUDED.disk_total,
    pve_version = EXCLUDED.pve_version,
    ssl_fingerprint = EXCLUDED.ssl_fingerprint,
    uptime = EXCLUDED.uptime,
    cpu_model = EXCLUDED.cpu_model,
    cpu_cores = EXCLUDED.cpu_cores,
    cpu_sockets = EXCLUDED.cpu_sockets,
    cpu_threads = EXCLUDED.cpu_threads,
    cpu_mhz = EXCLUDED.cpu_mhz,
    kernel_version = EXCLUDED.kernel_version,
    swap_total = EXCLUDED.swap_total,
    swap_used = EXCLUDED.swap_used,
    swap_free = EXCLUDED.swap_free,
    dns_servers = EXCLUDED.dns_servers,
    dns_search = EXCLUDED.dns_search,
    timezone = EXCLUDED.timezone,
    subscription_status = EXCLUDED.subscription_status,
    subscription_level = EXCLUDED.subscription_level,
    load_avg = EXCLUDED.load_avg,
    io_wait = EXCLUDED.io_wait,
    last_seen_at = now()
RETURNING *;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = $1;

-- name: GetNodeByClusterAndName :one
SELECT * FROM nodes WHERE cluster_id = $1 AND name = $2;

-- name: ListNodesByCluster :many
SELECT * FROM nodes WHERE cluster_id = $1 ORDER BY name;

-- name: UpdateNodeAddress :exec
UPDATE nodes SET address = $3, updated_at = now()
WHERE cluster_id = $1 AND name = $2 AND address != $3;

-- name: CountNodeStatusesByCluster :many
SELECT cluster_id,
       COUNT(*)::bigint AS total,
       COUNT(*) FILTER (WHERE status = 'online')::bigint AS online
FROM nodes
GROUP BY cluster_id;

-- name: GetNodeAddressByName :one
SELECT address FROM nodes WHERE cluster_id = $1 AND name = $2;

-- name: ListNodeAddresses :many
SELECT name, address FROM nodes WHERE cluster_id = $1 AND address != '' ORDER BY name;
