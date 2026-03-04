-- name: UpsertVM :one
INSERT INTO vms (cluster_id, node_id, vmid, name, type, status, cpu_count, mem_total, disk_total, uptime, template, tags, ha_state, pool, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now())
ON CONFLICT (cluster_id, vmid) DO UPDATE SET
    node_id = EXCLUDED.node_id,
    name = EXCLUDED.name,
    type = EXCLUDED.type,
    status = EXCLUDED.status,
    cpu_count = EXCLUDED.cpu_count,
    mem_total = EXCLUDED.mem_total,
    disk_total = EXCLUDED.disk_total,
    uptime = EXCLUDED.uptime,
    template = EXCLUDED.template,
    tags = EXCLUDED.tags,
    ha_state = EXCLUDED.ha_state,
    pool = EXCLUDED.pool,
    last_seen_at = now()
RETURNING *;

-- name: ListVMsByCluster :many
SELECT * FROM vms WHERE cluster_id = $1 ORDER BY vmid;

-- name: ListVMsByNode :many
SELECT * FROM vms WHERE node_id = $1 ORDER BY vmid;

-- name: GetVM :one
SELECT * FROM vms WHERE id = $1;

-- name: GetVMByClusterAndVmid :one
SELECT * FROM vms WHERE cluster_id = $1 AND vmid = $2;
