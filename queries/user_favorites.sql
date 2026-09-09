-- Favorites are keyed on the STABLE Proxmox identity, never on the surrogate
-- nodes.id/vms.id, because the collector re-inserts those rows with fresh UUIDs
-- whenever Proxmox transiently stops listing a resource (see the note in
-- migrations/000101). Each listing therefore joins back to the live row to
-- resolve the id the SPA has to route to *now* — the same trick
-- ListVMFolderMembershipsByCluster uses.
--
-- A favorite whose target isn't currently present drops out of the join rather
-- than erroring. That is the wanted behaviour: a guest mid-migration vanishes
-- from the list for a sync or two and comes back on its own, and the row itself
-- is never lost.
--
-- accessible_cluster_ids carries the caller's RBAC scope for the resource type
-- this query lists: NULL means global access (no restriction), an array
-- restricts rows, and '{}' matches nothing. cluster_id is NOT NULL here, so
-- there is no three-valued-logic case to reason about.

-- name: ListFavoriteClusters :many
SELECT f.cluster_id, f.created_at, c.name AS cluster_name
FROM user_favorites f
JOIN clusters c ON c.id = f.cluster_id
WHERE f.user_id = sqlc.arg('user_id')
  AND f.resource_type = 'cluster'
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR f.cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
ORDER BY c.name;

-- name: ListFavoriteNodes :many
SELECT f.cluster_id, f.created_at,
       c.name AS cluster_name,
       n.id AS node_id, n.name AS node_name, n.status, n.ha_state
FROM user_favorites f
JOIN clusters c ON c.id = f.cluster_id
JOIN nodes n ON n.cluster_id = f.cluster_id AND n.name = f.resource_ref
WHERE f.user_id = sqlc.arg('user_id')
  AND f.resource_type = 'node'
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR f.cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
ORDER BY c.name, n.name;

-- ListFavoriteVMs resolves resource_ref (a VMID held as text) back to an int to
-- join on the vms UNIQUE (cluster_id, vmid) index.
--
-- The CTE is MATERIALIZED so that resource_type = 'vm' is applied before
-- resource_ref::int is ever evaluated. A node favorite's ref is a node NAME, so
-- casting an unfiltered resource_ref raises "invalid input syntax for type
-- integer" — the CHECK in 000101 guarantees the digits only for the 'vm' rows.
--
-- Tested on PG16 with 400 name-shaped node refs alongside the vm rows: the
-- unmaterialized and no-CTE forms both survive, because a predicate over the
-- favourites table's own columns becomes a scan-level Filter that runs ahead of
-- the join condition, under a forced hash join as well as a nested loop. So
-- this is a fence against a plan shape that was NOT reproducible here, not a
-- fix for an observed break — keep it because MATERIALIZED is a documented
-- optimisation barrier (PG12+) that makes the ordering an explicit guarantee
-- rather than a property of the current planner, and the plan it produces is
-- the one wanted anyway (nested loop over the vms unique index).

-- name: ListFavoriteVMs :many
WITH favs AS MATERIALIZED (
    SELECT cluster_id, resource_ref, created_at
    FROM user_favorites
    WHERE user_id = sqlc.arg('user_id')
      AND resource_type = 'vm'
      AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
           OR cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
)
SELECT f.cluster_id, f.created_at,
       c.name AS cluster_name,
       v.id AS vm_id, v.vmid, v.name AS vm_name, v.type, v.status,
       v.template, v.ostype, v.config_ostype,
       n.name AS node_name
FROM favs f
JOIN clusters c ON c.id = f.cluster_id
JOIN vms v ON v.cluster_id = f.cluster_id AND v.vmid = f.resource_ref::int
-- The guest's current node, which the row's context menu needs to offer
-- migrate and console. vms.node_id is NOT NULL with an FK to nodes, so this
-- inner join can never drop a row the vms join already matched.
JOIN nodes n ON n.id = v.node_id
-- By VMID, not by name: the row renders as "<vmid> <name>", so the VMID is the
-- leading token the eye scans, and both sidebar trees sort guests by it
-- (InventoryTree and VMTree both `.sort((a, b) => a.vmid - b.vmid)`). Sorting
-- by name here would print a scrambled column of numbers directly above a tree
-- printing the same numbers in order.
ORDER BY c.name, v.vmid;

-- FavoriteTargetExists reports whether the thing being starred is actually
-- there.
--
-- Checked before every insert, and the bound on the whole table depends on it.
-- CountResolvableFavorites deliberately does NOT count rows whose target has
-- gone (they are unremovable through the UI, so counting them could lock a user
-- out) — which on its own would mean rows that never resolve are never counted
-- and so could be inserted without limit. Requiring the target to exist at
-- insert time closes that: every row costs a distinct real cluster, node or
-- guest the caller can already see, so the set is bounded by the estate rather
-- than by the caller.
--
-- The branches are mutually exclusive on resource_type, so exactly one arm can
-- return a row. vmid arrives as an int rather than being cast from the text ref,
-- which keeps this free of the ordering question ListFavoriteVMs has to fence.
-- name: FavoriteTargetExists :one
SELECT EXISTS (
    SELECT 1 FROM clusters c
     WHERE sqlc.arg('resource_type')::text = 'cluster'
       AND c.id = sqlc.arg('cluster_id')
    UNION ALL
    SELECT 1 FROM nodes n
     WHERE sqlc.arg('resource_type')::text = 'node'
       AND n.cluster_id = sqlc.arg('cluster_id')
       AND n.name = sqlc.arg('node_name')::text
    UNION ALL
    SELECT 1 FROM vms v
     WHERE sqlc.arg('resource_type')::text = 'vm'
       AND v.cluster_id = sqlc.arg('cluster_id')
       AND v.vmid = sqlc.arg('vmid')::int
) AS exists;

-- name: AddFavorite :exec
INSERT INTO user_favorites (user_id, cluster_id, resource_type, resource_ref)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, cluster_id, resource_type, resource_ref) DO NOTHING;

-- name: RemoveFavorite :exec
DELETE FROM user_favorites
WHERE user_id = $1 AND cluster_id = $2 AND resource_type = $3 AND resource_ref = $4;

-- CountResolvableFavorites is the user-facing half of the per-user bound: the
-- number the "limit reached" message is about. The half that actually bounds the
-- table is FavoriteTargetExists above, because this one deliberately does not
-- count rows that a determined caller could otherwise mint at will.
--
-- It counts only favorites whose target currently RESOLVES — the same joins the
-- listings use — and that is the whole point of the query. A bare COUNT(*) would
-- count rows the user cannot see and therefore cannot remove: destroy a starred
-- guest and its row is stranded: every unstar affordance hangs off a row that
-- renders, and none of them do. Enough of those and the cap refuses
-- new favorites while the sidebar shows almost none, with no way out. Counting
-- what is visible means the ceiling can always be cleared by unstarring
-- something the user can actually see.
--
-- Stranded rows are left in place rather than swept, deliberately: a guest is
-- unresolvable for a sync or two during a live migration, and pruning on that
-- signal would permanently unstar guests for migrating.
-- name: CountResolvableFavorites :one
-- Same MATERIALIZED fence, and for the same reason, as ListFavoriteVMs.
WITH vm_favs AS MATERIALIZED (
    SELECT cluster_id, resource_ref FROM user_favorites
    WHERE user_id = $1 AND resource_type = 'vm'
)
SELECT (
    SELECT COUNT(*) FROM user_favorites f
    JOIN clusters c ON c.id = f.cluster_id
    WHERE f.user_id = $1 AND f.resource_type = 'cluster'
) + (
    SELECT COUNT(*) FROM user_favorites f
    JOIN nodes n ON n.cluster_id = f.cluster_id AND n.name = f.resource_ref
    WHERE f.user_id = $1 AND f.resource_type = 'node'
) + (
    SELECT COUNT(*) FROM vm_favs f
    JOIN vms v ON v.cluster_id = f.cluster_id AND v.vmid = f.resource_ref::int
) AS count;
