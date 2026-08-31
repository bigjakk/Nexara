-- name: UpsertVirtioWinRelease :one
--
-- checksum/checksum_algorithm are operator-supplied and are deliberately NOT
-- overwritten by the upstream refresh. The refresh knows the URL and the size;
-- it never learns a hash, because upstream publishes none for the ISO. Letting
-- EXCLUDED win here would silently erase a checksum an operator had pasted in
-- the moment the next 6-hourly catalog refresh ran.
INSERT INTO virtio_win_releases (
    version, iso_version, iso_filename, iso_url, iso_size, is_stable, published_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (version) DO UPDATE SET
    iso_version  = EXCLUDED.iso_version,
    iso_filename = EXCLUDED.iso_filename,
    iso_url      = EXCLUDED.iso_url,
    -- Coalesce rather than assert: only the release being probed carries a real
    -- size, and the archive sweep restates every OTHER release with 0. Asserting
    -- here wipes a previously-probed size the moment a version stops being the
    -- latest, so a pinned older release would show no size at all.
    iso_size     = CASE WHEN EXCLUDED.iso_size > 0
                        THEN EXCLUDED.iso_size
                        ELSE virtio_win_releases.iso_size END,
    is_stable    = EXCLUDED.is_stable,
    published_at = COALESCE(EXCLUDED.published_at, virtio_win_releases.published_at)
RETURNING *;

-- name: GetVirtioWinRelease :one
SELECT * FROM virtio_win_releases WHERE version = $1;

-- name: ListVirtioWinReleases :many
SELECT * FROM virtio_win_releases ORDER BY discovered_at DESC, version DESC;

-- name: GetStableVirtioWinRelease :one
SELECT * FROM virtio_win_releases WHERE is_stable ORDER BY version DESC LIMIT 1;

-- ClearVirtioWinStableFlag runs immediately before marking the newly-discovered
-- stable release, so the partial index on is_stable only ever matches one row.
-- name: ClearVirtioWinStableFlag :exec
UPDATE virtio_win_releases SET is_stable = false WHERE is_stable AND version <> $1;

-- name: SetVirtioWinReleaseChecksum :exec
UPDATE virtio_win_releases
SET checksum = $2, checksum_algorithm = $3
WHERE version = $1;

-- name: GetVirtioWinConfig :one
SELECT * FROM virtio_win_configs WHERE cluster_id = $1;

-- name: UpsertVirtioWinConfig :one
--
-- prune_enabled follows the omit-vs-assert idiom from UpsertDRSConfig: it is a
-- destructive opt-in, so an absent key preserves the stored value rather than
-- reading as false. A client that predates the field cannot arm it, and a stale
-- browser tab saving an unrelated storage change cannot disarm it.
INSERT INTO virtio_win_configs (cluster_id, enabled, storage, node, target_version, prune_enabled)
VALUES ($1, $2, $3, $4, $5, COALESCE(sqlc.narg('prune_enabled')::boolean, false))
ON CONFLICT (cluster_id) DO UPDATE SET
    enabled        = EXCLUDED.enabled,
    storage        = EXCLUDED.storage,
    node           = EXCLUDED.node,
    target_version = EXCLUDED.target_version,
    prune_enabled  = COALESCE(sqlc.narg('prune_enabled')::boolean, virtio_win_configs.prune_enabled)
RETURNING *;

-- name: ListEnabledVirtioWinConfigs :many
SELECT * FROM virtio_win_configs WHERE enabled AND storage <> '';

-- name: MarkVirtioWinConfigChecked :exec
UPDATE virtio_win_configs
SET last_check_at = now(), last_error = $2
WHERE cluster_id = $1;

-- name: DeleteVirtioWinConfig :exec
DELETE FROM virtio_win_configs WHERE cluster_id = $1;

-- ListPinnedVirtioWinVersions returns every version any cluster has pinned.
-- The prune keep-set is built from this plus the newest release, so a pin held
-- by one cluster protects that ISO on every cluster.
-- name: ListPinnedVirtioWinVersions :many
SELECT DISTINCT target_version FROM virtio_win_configs WHERE target_version <> '';

-- name: InsertVirtioWinDownload :one
INSERT INTO virtio_win_downloads (
    cluster_id, node, storage, version, filename, status, upid, triggered_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- SetVirtioWinDownloadUPID records the Proxmox task once the node has accepted
-- it. The row is inserted BEFORE the download-url call so the partial unique
-- index on unfinished (cluster, storage, version) can reject a concurrent
-- duplicate; the UPID only exists after that call returns.
-- name: SetVirtioWinDownloadUPID :exec
UPDATE virtio_win_downloads
SET upid = $2, status = 'running'
WHERE id = $1;

-- name: ListActiveVirtioWinDownloads :many
SELECT * FROM virtio_win_downloads
WHERE status IN ('pending', 'running')
ORDER BY started_at;

-- name: ListVirtioWinDownloadsByCluster :many
SELECT * FROM virtio_win_downloads
WHERE cluster_id = $1
ORDER BY started_at DESC
LIMIT $2;

-- name: CountVirtioWinDownloadsByCluster :one
SELECT COUNT(*) FROM virtio_win_downloads WHERE cluster_id = $1;

-- name: FinishVirtioWinDownload :exec
UPDATE virtio_win_downloads
SET status = $2, error = $3, finished_at = now()
WHERE id = $1;

-- DeleteOldVirtioWinDownloads trims finished history, keeping the table bounded
-- without touching anything still in flight.
-- name: DeleteOldVirtioWinDownloads :exec
DELETE FROM virtio_win_downloads
WHERE status IN ('succeeded', 'failed') AND finished_at < $1;

-- ListNodesWithStorage returns the online nodes that actually carry a given
-- storage, so a download is dispatched somewhere it can succeed.
--
-- download-url is node-scoped even though the ISO lands on the storage, and a
-- storage restricted to a subset of nodes (any local directory pool) fails on
-- the rest. Picking "any online node" is only correct for shared storage.
-- name: ListNodesWithStorage :many
SELECT n.name
FROM storage_pools sp
JOIN nodes n ON n.id = sp.node_id
WHERE sp.cluster_id = $1
  AND sp.storage = $2
  AND sp.active
  AND sp.enabled
  AND lower(n.status) = 'online'
ORDER BY n.name;
