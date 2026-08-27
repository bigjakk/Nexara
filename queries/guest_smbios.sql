-- Cache of each QEMU guest's smbios1 uuid — the deterministic join key between
-- Nexara's guest inventory and a Veeam backup object.
--
-- Keyed on (cluster_id, vmid), never vms.id: the collector deletes and
-- re-inserts guest rows with fresh UUIDs on churn, which would empty a cache
-- keyed on that id and force a full per-guest config re-fetch every time.

-- name: UpsertGuestSmbios :exec
-- smbios_uuid is lowered here as well as in the caller so the stored value is
-- canonical no matter which path writes it. The correlation join lowers both
-- sides too — a case mismatch would degrade a deterministic match to the
-- low-confidence name tier with nothing to indicate why.
INSERT INTO guest_smbios (cluster_id, vmid, smbios_uuid, last_seen_at)
VALUES ($1, $2, lower(@smbios_uuid::text), now())
ON CONFLICT (cluster_id, vmid)
DO UPDATE SET
    smbios_uuid  = EXCLUDED.smbios_uuid,
    last_seen_at = now();

-- name: ListGuestSmbiosByCluster :many
SELECT * FROM guest_smbios WHERE cluster_id = $1 ORDER BY vmid;

-- DeleteGuestSmbiosForVanishedGuests drops cache rows for guests that are no
-- longer in the cluster's vms inventory. vmids come from the vms table rather
-- than a live listing, so this inherits the grace protection
-- DeleteStaleVMsForNodes gives those rows and a transient Proxmox blip cannot
-- flush the cache. The list must be a non-nil (possibly empty) slice: pgx
-- encodes nil as SQL NULL and NOT (x = ANY(NULL)) is NULL, so a nil list
-- silently deletes nothing.
-- name: DeleteGuestSmbiosForVanishedGuests :execrows
DELETE FROM guest_smbios
WHERE cluster_id = $1
  AND NOT (vmid = ANY(@vmids::int[]));

-- ListClustersWithVeeamPlatform returns the clusters an operator has mapped a
-- Veeam platform to. The per-guest config fetch that fills guest_smbios runs
-- only for these: a deployment with no Veeam server, or one whose platform is
-- not mapped yet, pays nothing for a correlation it cannot use.
-- name: ListClustersWithVeeamPlatform :many
SELECT DISTINCT c.*
FROM clusters c
JOIN veeam_platforms p ON p.cluster_id = c.id
JOIN veeam_servers s ON s.id = p.veeam_server_id AND s.enabled
WHERE c.is_active
ORDER BY c.name;
