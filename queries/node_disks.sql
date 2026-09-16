-- name: UpsertNodeDisks :exec
-- One statement — and therefore one transaction and at most one WAL flush —
-- for a whole node's physical-disk inventory, instead of one implicit
-- transaction per disk. See UpsertNodePCIDevices for the full rationale.
--
-- The DO UPDATE is gated: an unchanged disk whose last_seen_at is still inside
-- the heartbeat window writes NOTHING, so a sweep over unchanged hardware
-- produces no dirty tuples, no WAL and no fsync. @heartbeat_seconds MUST stay
-- well below the DeleteStaleNodeDisks grace window, or the prune below would
-- delete rows the heartbeat has not refreshed yet.
--
-- Note health/wearout are SMART-derived and do move on their own, so this
-- table is not as static as PCI devices; the content gate handles that — a
-- real SMART change writes immediately, it is only the idle case that is free.
--
-- DISTINCT ON dedupes the input on the conflict key: Postgres rejects an
-- ON CONFLICT DO UPDATE that would touch the same row twice in one statement.
-- The batch arrives as one jsonb array rather than N parallel array parameters
-- because sqlc cannot parse multi-argument unnest(...).
INSERT INTO node_disks (node_id, cluster_id, dev_path, model, serial, size, disk_type, health, wearout, rpm, vendor, wwn, last_seen_at)
SELECT DISTINCT ON (k.dev_path)
       @node_id::uuid, @cluster_id::uuid, k.dev_path, k.model, k.serial, k.size, k.disk_type,
       k.health, k.wearout, k.rpm, k.vendor, k.wwn, now()
FROM jsonb_to_recordset(@disks::jsonb) AS k(
        dev_path text, model text, serial text, size bigint, disk_type text,
        health text, wearout text, rpm int, vendor text, wwn text
     )
ORDER BY k.dev_path
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
    -- last_seen_at marks every sync that gets this far; updated_at moves only when the
    -- row's content actually changed, so it answers "when did this row last change?"
    -- rather than "when was it last polled?". The WHERE below lets a heartbeat-only
    -- refresh through, so this CASE is still what keeps updated_at honest.
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
WHERE (
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
) OR node_disks.last_seen_at < now() - make_interval(secs => @heartbeat_seconds::int);

-- name: ListNodeDisksByNode :many
SELECT * FROM node_disks WHERE node_id = $1 ORDER BY dev_path;

-- name: DeleteStaleNodeDisks :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStaleVMsForNodes in vms.sql):
-- a momentary non-observation no longer churns physical-disk rows.
-- The grace window MUST exceed the upsert's @heartbeat_seconds above.
DELETE FROM node_disks
WHERE node_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);
