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
    -- last_seen_at marks every sync; updated_at moves only when the row's content actually
    -- changed, so it answers "when did this row last change?" rather than "when was it last polled?".
    updated_at = CASE WHEN (
        node_disks.model,
        node_disks.serial,
        node_disks.size,
        node_disks.disk_type,
        node_disks.health,
        node_disks.wearout,
        node_disks.rpm,
        node_disks.vendor,
        node_disks.wwn
    ) IS DISTINCT FROM (
        EXCLUDED.model,
        EXCLUDED.serial,
        EXCLUDED.size,
        EXCLUDED.disk_type,
        EXCLUDED.health,
        EXCLUDED.wearout,
        EXCLUDED.rpm,
        EXCLUDED.vendor,
        EXCLUDED.wwn
    ) THEN now() ELSE node_disks.updated_at END,
    last_seen_at = now()
RETURNING *;

-- name: ListNodeDisksByNode :many
SELECT * FROM node_disks WHERE node_id = $1 ORDER BY dev_path;

-- name: DeleteStaleNodeDisks :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStaleVMsForNodes in vms.sql):
-- a momentary non-observation no longer churns physical-disk rows.
DELETE FROM node_disks
WHERE node_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);
