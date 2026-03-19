-- name: UpsertNodeDisk :one
INSERT INTO node_disks (node_id, cluster_id, dev_path, model, serial, size, disk_type, health, wearout, rpm, vendor, wwn, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
ON CONFLICT (node_id, dev_path) DO UPDATE SET
    model = EXCLUDED.model,
    serial = EXCLUDED.serial,
    size = EXCLUDED.size,
    disk_type = EXCLUDED.disk_type,
    health = EXCLUDED.health,
    wearout = EXCLUDED.wearout,
    rpm = EXCLUDED.rpm,
    vendor = EXCLUDED.vendor,
    wwn = EXCLUDED.wwn,
    last_seen_at = now()
RETURNING *;

-- name: ListNodeDisksByNode :many
SELECT * FROM node_disks WHERE node_id = $1 ORDER BY dev_path;

-- name: DeleteStaleNodeDisks :exec
DELETE FROM node_disks WHERE node_id = $1 AND last_seen_at < $2;
