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

-- ClearVirtioWinStableFlag reasserts the flag after a catalog refresh, so the
-- partial index on is_stable only ever matches the row the source just named.
--
-- Passing '' clears EVERY row, which is the answer when the source could not
-- name a stable version at all — a mirror has no stable-virtio/ redirect to
-- copy. No version string is empty, so the <> holds nothing back.
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
--
-- next_check_at is decided here rather than by the caller so that a save and a
-- scheduler tick cannot interleave into a lost update. Three outcomes, in the
-- order the CASE tests them:
--
--   NULL ("due now") when the cluster has just been switched on, or when the
--   storage or pinned version changed while it was on. The operator has just
--   stated what they want held; waiting until 03:00 to act on it reads as the
--   save not having worked.
--
--   The caller's freshly computed time when only the schedule or its zone
--   changed. Recomputing is the whole point of that edit, and it must not
--   trigger a check as a side effect.
--
--   Otherwise unchanged, so that saving an unrelated field (prune, node) does
--   not reset the cycle. A row that keeps being saved every few minutes would
--   otherwise never reach its own next check.
--
-- The NULL passthrough ahead of the schedule branch keeps a check that is
-- already due, due. Enabling and then setting the schedule is two saves, and
-- without it the second would push the first one's pending check out to 03:00
-- — so the sync the operator just asked for would silently not happen.
INSERT INTO virtio_win_configs (
    cluster_id, enabled, storage, node, target_version, prune_enabled,
    check_schedule, check_timezone
)
VALUES (
    $1, $2, $3, $4, $5, COALESCE(sqlc.narg('prune_enabled')::boolean, false),
    sqlc.arg('check_schedule'), sqlc.arg('check_timezone')
)
ON CONFLICT (cluster_id) DO UPDATE SET
    enabled        = EXCLUDED.enabled,
    storage        = EXCLUDED.storage,
    node           = EXCLUDED.node,
    target_version = EXCLUDED.target_version,
    prune_enabled  = COALESCE(sqlc.narg('prune_enabled')::boolean, virtio_win_configs.prune_enabled),
    check_schedule = EXCLUDED.check_schedule,
    check_timezone = EXCLUDED.check_timezone,
    next_check_at  = CASE
        WHEN NOT EXCLUDED.enabled THEN NULL
        WHEN NOT virtio_win_configs.enabled
             OR virtio_win_configs.storage        IS DISTINCT FROM EXCLUDED.storage
             OR virtio_win_configs.target_version IS DISTINCT FROM EXCLUDED.target_version
            THEN NULL
        WHEN virtio_win_configs.next_check_at IS NULL THEN NULL
        WHEN virtio_win_configs.check_schedule IS DISTINCT FROM EXCLUDED.check_schedule
             OR virtio_win_configs.check_timezone IS DISTINCT FROM EXCLUDED.check_timezone
            THEN sqlc.narg('next_check_at')::timestamptz
        ELSE virtio_win_configs.next_check_at
    END
RETURNING *;

-- ListDueVirtioWinConfigs returns the opted-in clusters whose next check has
-- come round. NULL is "due now": that is what a fresh row, a just-enabled
-- cluster, and a pre-000100 row upgraded in place all carry, so each gets one
-- check promptly and a schedule from then on.
-- name: ListDueVirtioWinConfigs :many
SELECT * FROM virtio_win_configs
WHERE enabled
  AND storage <> ''
  AND (next_check_at IS NULL OR next_check_at <= now());

-- MarkVirtioWinConfigChecked records the outcome and arms the next check in one
-- statement. Splitting them would let a crash between the two leave a row whose
-- next_check_at is still in the past, i.e. one that re-checks on every tick.
-- name: MarkVirtioWinConfigChecked :exec
UPDATE virtio_win_configs
SET last_check_at = now(),
    last_error    = $2,
    next_check_at = sqlc.narg('next_check_at')::timestamptz
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
