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

-- name: ListContainersByCluster :many
SELECT * FROM vms WHERE cluster_id = $1 AND type = 'lxc' ORDER BY vmid;

-- name: GetContainer :one
SELECT * FROM vms WHERE id = $1 AND type = 'lxc';

-- name: UpdateVMStatus :exec
UPDATE vms SET status = $2, updated_at = now() WHERE id = $1;

-- name: SetVMOSType :exec
UPDATE vms SET ostype = $2, updated_at = now() WHERE id = $1;

-- name: SetVMConfigOSType :exec
UPDATE vms SET config_ostype = $2, updated_at = now() WHERE id = $1;

-- ListVMStatusesByCluster feeds the collector's pre/post-sync inventory diff.
-- Every column here is compared across a sync pass to decide whether to
-- publish an inventory_change event, so external edits (Proxmox UI, qm/pct)
-- become visible to the frontend within one tick.
-- name: ListVMStatusesByCluster :many
SELECT id, vmid, node_id, status, name, template, pool, ha_state, tags
FROM vms WHERE cluster_id = $1;

-- name: ListAllVMs :many
SELECT v.*, c.name AS cluster_name
FROM vms v
JOIN clusters c ON c.id = v.cluster_id
WHERE v.template = false
ORDER BY v.name;

-- name: UpdateVMPool :exec
UPDATE vms SET pool = $2 WHERE id = $1;

-- name: DeleteStaleVMs :exec
DELETE FROM vms WHERE cluster_id = $1 AND last_seen_at < $2;

-- name: DeleteStaleVMsForNodes :execrows
-- Prunes VMs only on nodes that synced successfully this cycle, and only once a
-- VM has been unseen for longer than the grace window. Both guards matter:
--   * node scoping stops a transient per-node Proxmox API failure from wiping
--     that node's inventory;
--   * the grace window (evaluated entirely on the DB clock via now()) stops a
--     momentary non-observation — e.g. the cutover instant of a live migration,
--     when Proxmox briefly lists the guest on neither source nor destination —
--     from deleting and re-inserting the row. That churn would mint a fresh
--     vms.id and, before migration 000068, silently dropped folder memberships.
--     Driving both sides from now() also avoids mixing the app and DB clocks.
DELETE FROM vms
WHERE cluster_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int)
  AND node_id = ANY(@node_ids::uuid[]);

-- DeleteVMsAbsentFromCluster removes guests that are no longer present in the
-- cluster configuration. The fast resource-sync loop feeds this from
-- GET /cluster/resources, which is config-authoritative: a guest stays listed
-- there throughout live migrations and HA recovery and disappears only when it
-- is actually destroyed — so unlike the per-node listing path there is no
-- transient-blip churn risk and no grace window is needed. An empty vmid list
-- is valid (a genuinely empty cluster prunes everything); callers must verify
-- the resources payload was well-formed before treating it as authoritative.
-- name: DeleteVMsAbsentFromCluster :execrows
DELETE FROM vms
WHERE cluster_id = $1
  AND NOT (vmid = ANY(@vmids::int[]));
