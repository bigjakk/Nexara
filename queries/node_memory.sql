-- name: ListNodeMemoryReadState :many
-- Every node of an active cluster with when its memory was last tried and how
-- that went, for the daily pass. A paused cluster (is_active false) is the
-- operator saying "leave it alone": the collector skips it, and so does this.
SELECT n.id AS node_id, n.cluster_id, n.name, n.status,
       i.status AS read_status, i.attempted_at
FROM nodes n
JOIN clusters c ON c.id = n.cluster_id AND c.is_active
LEFT JOIN node_memory_inventory i ON i.node_id = n.id
ORDER BY n.cluster_id, n.name;

-- name: UpsertNodeMemoryRead :exec
-- A good read replaces the node's row.
INSERT INTO node_memory_inventory (
    node_id, status, reason, attempted_at, read_at,
    installed_bytes, slots_used, slots_total, max_capacity_bytes, ecc, modules
) VALUES (
    @node_id, 'ok', '', @read_at::timestamptz, @read_at::timestamptz,
    @installed_bytes, @slots_used, @slots_total, @max_capacity_bytes, @ecc, @modules
)
ON CONFLICT (node_id) DO UPDATE SET
    status = 'ok', reason = '', attempted_at = EXCLUDED.attempted_at, read_at = EXCLUDED.read_at,
    installed_bytes = EXCLUDED.installed_bytes, slots_used = EXCLUDED.slots_used,
    slots_total = EXCLUDED.slots_total, max_capacity_bytes = EXCLUDED.max_capacity_bytes,
    ecc = EXCLUDED.ecc, modules = EXCLUDED.modules;

-- name: RecordNodeMemoryFailure :exec
-- A failed read records why and when, and keeps the last good read.
INSERT INTO node_memory_inventory (node_id, status, reason, attempted_at)
VALUES (@node_id, @status, @reason, @attempted_at)
ON CONFLICT (node_id) DO UPDATE SET
    status = EXCLUDED.status, reason = EXCLUDED.reason, attempted_at = EXCLUDED.attempted_at;

-- name: GetNodeMemoryInventory :one
SELECT * FROM node_memory_inventory WHERE node_id = $1;

-- name: ListNodeMemoryInventoryByCluster :many
-- The nodes of a cluster with a good memory read, for the capacity forecast,
-- with the memory the node's kernel reports: a firmware table that lists less
-- (a virtual node's hot-plugged memory) is incomplete.
SELECT n.name AS node_name, n.mem_total AS node_mem_total, i.installed_bytes, i.slots_used, i.slots_total,
       i.max_capacity_bytes, i.modules, i.read_at
FROM node_memory_inventory i
JOIN nodes n ON n.id = i.node_id
WHERE n.cluster_id = $1 AND i.read_at IS NOT NULL
ORDER BY n.name;
