-- What each UPS powers, and the upsmon logins that say whether it is
-- protected (000132).

-- name: UpsertUpsClients :exec
-- One LIST CLIENT reply, one address per connection as upsd lists them: the
-- repeats are counted here. seen_at is the poller's clock, like the other
-- times it writes, so PruneUpsClients compares like with like.
INSERT INTO ups_clients (ups_id, address, connections, first_seen_at, last_seen_at)
SELECT @ups_id::uuid, l.address, LEAST(count(*), 32767)::smallint,
       @seen_at::timestamptz, @seen_at::timestamptz
FROM unnest(@addresses::text[]) AS l(address)
GROUP BY l.address
ON CONFLICT (ups_id, address) DO UPDATE
SET connections = EXCLUDED.connections,
    missed = 0,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: MarkUpsClientsMissed :exec
-- Every other address of the UPS missed this listing.
UPDATE ups_clients
SET connections = 0,
    missed = LEAST(missed + 1, 32767)
WHERE ups_id = @ups_id
  AND NOT (address = ANY(COALESCE(@addresses::text[], '{}')));

-- name: PruneUpsClients :exec
-- Logins not seen since before, so a host that changed address does not stay
-- listed forever.
DELETE FROM ups_clients WHERE ups_id = @ups_id AND last_seen_at < @before;

-- name: ListUpsClients :many
-- Every login of every present device.
SELECT c.*
FROM ups_clients c
JOIN ups_devices d ON d.id = c.ups_id
WHERE d.present
ORDER BY c.ups_id, c.address;

-- name: ListUpsFeeds :many
-- Every feed of every present device, removed ones aside: nodes first, then
-- other loads.
SELECT f.*
FROM ups_feeds f
JOIN ups_devices d ON d.id = f.ups_id
WHERE d.present AND NOT f.dismissed
ORDER BY f.ups_id, f.cluster_id IS NULL, f.node_name, lower(f.label);

-- name: InsertFoundUpsFeed :execrows
-- A node a login came from. A feed that already exists is left as it is: one
-- added by hand, and one the operator removed, which stays removed.
INSERT INTO ups_feeds (ups_id, cluster_id, node_name, source)
VALUES (@ups_id, @cluster_id::uuid, @node_name::text, 'found')
ON CONFLICT (ups_id, cluster_id, node_name) DO NOTHING;

-- name: InsertManualUpsFeed :one
-- A node or a label added by hand. A removed node comes back; one already fed
-- returns no row, and a label already there is a unique violation.
INSERT INTO ups_feeds (ups_id, cluster_id, node_name, label, source, created_by)
VALUES (@ups_id, sqlc.narg(cluster_id), sqlc.narg(node_name), @label, 'manual', @created_by::uuid)
ON CONFLICT (ups_id, cluster_id, node_name) DO UPDATE
SET dismissed = FALSE,
    source = 'manual',
    created_by = EXCLUDED.created_by,
    created_at = EXCLUDED.created_at
WHERE ups_feeds.dismissed
RETURNING *;

-- name: DismissUpsNodeFeed :one
-- Removing a node feed keeps the row, so the node's login does not find it again.
UPDATE ups_feeds
SET dismissed = TRUE
WHERE id = @id AND ups_id = @ups_id AND cluster_id IS NOT NULL AND NOT dismissed
RETURNING *;

-- name: DeleteUpsLabelFeed :one
DELETE FROM ups_feeds WHERE id = @id AND ups_id = @ups_id AND cluster_id IS NULL
RETURNING *;

-- name: ListNodeLoginAddresses :many
-- Every address a node can log in to upsd from, to match LIST CLIENT against:
-- its cluster (corosync) address, which may be IPv6, and its interfaces' IPv4
-- addresses.
SELECT n.cluster_id, n.name AS node_name, n.address
FROM nodes n
WHERE n.address <> ''
UNION
SELECT n.cluster_id, n.name AS node_name, i.address
FROM node_network_interfaces i
JOIN nodes n ON n.id = i.node_id
WHERE i.address <> ''
ORDER BY 1, 2, 3;

-- name: SetNutServerHostNode :exec
-- The node a NUT server runs on, as the poller found it. One already set, by
-- the poller or by hand, is kept.
UPDATE nut_servers
SET host_cluster_id = @cluster_id::uuid,
    host_node_name = @node_name::text
WHERE id = @id AND host_cluster_id IS NULL;
