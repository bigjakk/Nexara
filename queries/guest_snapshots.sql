-- name: UpsertGuestSnapshot :one
INSERT INTO guest_snapshots (cluster_id, vmid, name, guest_type, node, description, parent, vmstate, snap_time, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (cluster_id, vmid, name)
DO UPDATE SET
    guest_type = EXCLUDED.guest_type,
    node = EXCLUDED.node,
    description = EXCLUDED.description,
    parent = EXCLUDED.parent,
    vmstate = EXCLUDED.vmstate,
    snap_time = EXCLUDED.snap_time,
    last_seen_at = now()
RETURNING *;

-- name: ListGuestSnapshotsByCluster :many
SELECT * FROM guest_snapshots
WHERE cluster_id = $1
ORDER BY vmid, name;

-- DeleteGuestSnapshotsNotInSet removes a single guest's snapshots that are
-- absent from a freshly listed set. The snapshot listing endpoint is
-- config-authoritative (it reads the guest's config file), so no grace window
-- is needed — but callers must only invoke this for guests whose listing
-- succeeded this pass, and must treat a raw-empty listing as anomalous (PVE
-- always returns at least the synthetic "current" entry). An empty name list
-- is valid: a guest whose real snapshots were all deleted prunes everything —
-- but it must be a non-nil empty slice. pgx encodes a nil slice as SQL NULL,
-- and NOT (x = ANY(NULL)) is NULL, so a nil list silently deletes nothing.
-- name: DeleteGuestSnapshotsNotInSet :execrows
DELETE FROM guest_snapshots
WHERE cluster_id = $1
  AND vmid = $2
  AND NOT (name = ANY(@names::text[]));

-- DeleteGuestSnapshotsForVanishedGuests removes snapshot rows for guests that
-- no longer exist in the cluster's vms inventory. Callers feed vmids from the
-- vms table (not from a live listing), so this inherits the grace protection
-- DeleteStaleVMsForNodes gives vms rows — a transient per-node Proxmox blip
-- cannot cascade into snapshot-inventory loss. The vmid list must be a
-- non-nil (possibly empty) slice: pgx encodes nil as SQL NULL and the DELETE
-- then silently matches nothing.
-- name: DeleteGuestSnapshotsForVanishedGuests :execrows
DELETE FROM guest_snapshots
WHERE cluster_id = $1
  AND NOT (vmid = ANY(@vmids::int[]));

-- ListAllGuestSnapshots feeds the central snapshots page. vms is LEFT JOINed
-- on the stable (cluster_id, vmid) identity so rows whose guest row is mid-
-- churn (or gone) still render; vm_id/vm_name/vm_status are NULL then and the
-- frontend disables the guest link. Unknown ages (snap_time = 0) sort last.
-- name: ListAllGuestSnapshots :many
SELECT
    gs.cluster_id,
    gs.vmid,
    gs.name,
    gs.guest_type,
    gs.node,
    gs.description,
    gs.parent,
    gs.vmstate,
    gs.snap_time,
    gs.last_seen_at,
    c.name   AS cluster_name,
    v.id     AS vm_id,
    v.name   AS vm_name,
    v.status AS vm_status
FROM guest_snapshots gs
JOIN clusters c ON c.id = gs.cluster_id
LEFT JOIN vms v ON v.cluster_id = gs.cluster_id AND v.vmid = gs.vmid
ORDER BY (gs.snap_time = 0), gs.snap_time ASC, gs.cluster_id, gs.vmid, gs.name;

-- ListGuestSnapshotsForReport feeds the snapshot_inventory report type: one
-- cluster's rows, oldest dated first (unknown ages last), with the guest
-- name rejoined live (NULL when the guest is gone from inventory).
-- name: ListGuestSnapshotsForReport :many
SELECT
    gs.vmid,
    gs.name,
    gs.guest_type,
    gs.node,
    gs.description,
    gs.vmstate,
    gs.snap_time,
    v.name AS vm_name
FROM guest_snapshots gs
LEFT JOIN vms v ON v.cluster_id = gs.cluster_id AND v.vmid = gs.vmid
WHERE gs.cluster_id = $1
ORDER BY (gs.snap_time = 0), gs.snap_time ASC, gs.vmid, gs.name;

-- GetClusterSnapshotAgeStats backs the snapshot_age_days alert metric for
-- cluster-scoped rules: the oldest dated snapshot in the cluster plus how many
-- exceed the rule threshold. Rows with snap_time = 0 (age unknown) are
-- excluded — computing an age from 0 would read as ~56 years and false-fire.
-- Zero dated snapshots → no row (ErrNoRows), which callers treat as
-- condition-not-met so the alert auto-resolves after cleanup.
-- name: GetClusterSnapshotAgeStats :one
SELECT
    o.name AS oldest_name,
    o.vmid AS oldest_vmid,
    ((extract(epoch FROM now()) - o.snap_time) / 86400.0)::float8 AS oldest_age_days,
    (SELECT count(*)
       FROM guest_snapshots gs
      WHERE gs.cluster_id = $1
        AND gs.snap_time > 0
        AND (extract(epoch FROM now()) - gs.snap_time) / 86400.0 > @threshold_days::float8
    ) AS over_count
FROM guest_snapshots o
WHERE o.cluster_id = $1
  AND o.snap_time > 0
ORDER BY o.snap_time ASC
LIMIT 1;

-- GetVMSnapshotAgeStats is the vm-scoped counterpart of
-- GetClusterSnapshotAgeStats; same snap_time > 0 and ErrNoRows semantics.
-- name: GetVMSnapshotAgeStats :one
SELECT
    o.name AS oldest_name,
    o.vmid AS oldest_vmid,
    ((extract(epoch FROM now()) - o.snap_time) / 86400.0)::float8 AS oldest_age_days,
    (SELECT count(*)
       FROM guest_snapshots gs
      WHERE gs.cluster_id = $1
        AND gs.vmid = @vmid
        AND gs.snap_time > 0
        AND (extract(epoch FROM now()) - gs.snap_time) / 86400.0 > @threshold_days::float8
    ) AS over_count
FROM guest_snapshots o
WHERE o.cluster_id = $1
  AND o.vmid = @vmid
  AND o.snap_time > 0
ORDER BY o.snap_time ASC
LIMIT 1;
