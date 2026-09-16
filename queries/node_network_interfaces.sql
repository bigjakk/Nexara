-- name: UpsertNodeNetworkInterfaces :exec
-- One statement — and therefore one transaction and at most one WAL flush —
-- for a whole node's network inventory, instead of one implicit transaction
-- per interface. See UpsertNodePCIDevices for the full rationale.
--
-- The DO UPDATE is gated: an unchanged interface whose last_seen_at is still
-- inside the heartbeat window writes NOTHING, so a sweep over unchanged
-- hardware produces no dirty tuples, no WAL and no fsync. @heartbeat_seconds
-- MUST stay well below the DeleteStaleNodeNetworkInterfaces grace window, or
-- the prune below would delete rows the heartbeat has not refreshed yet.
--
-- DISTINCT ON dedupes the input on the conflict key: Postgres rejects an
-- ON CONFLICT DO UPDATE that would touch the same row twice in one statement.
-- The batch arrives as one jsonb array rather than N parallel array parameters
-- because sqlc cannot parse multi-argument unnest(...).
INSERT INTO node_network_interfaces (node_id, cluster_id, iface, iface_type, active, autostart, method, method6,
                                      address, netmask, gateway, cidr, bridge_ports, comments, mtu, last_seen_at)
SELECT DISTINCT ON (n.iface)
       @node_id::uuid, @cluster_id::uuid, n.iface, n.iface_type, n.active, n.autostart, n.method, n.method6,
       n.address, n.netmask, n.gateway, n.cidr, n.bridge_ports, n.comments, n.mtu, now()
FROM jsonb_to_recordset(@interfaces::jsonb) AS n(
        iface text, iface_type text, active boolean, autostart boolean,
        method text, method6 text, address text, netmask text, gateway text,
        cidr text, bridge_ports text, comments text, mtu int
     )
ORDER BY n.iface
ON CONFLICT (node_id, iface) DO UPDATE SET
    iface_type = EXCLUDED.iface_type,
    active = EXCLUDED.active,
    autostart = EXCLUDED.autostart,
    method = EXCLUDED.method,
    method6 = EXCLUDED.method6,
    address = EXCLUDED.address,
    netmask = EXCLUDED.netmask,
    gateway = EXCLUDED.gateway,
    cidr = EXCLUDED.cidr,
    bridge_ports = EXCLUDED.bridge_ports,
    comments = EXCLUDED.comments,
    mtu = EXCLUDED.mtu,
    -- last_seen_at marks every sync that gets this far; updated_at moves only when the
    -- row's content actually changed, so it answers "when did this row last change?"
    -- rather than "when was it last polled?". The WHERE below lets a heartbeat-only
    -- refresh through, so this CASE is still what keeps updated_at honest.
    updated_at = CASE WHEN (
        node_network_interfaces.iface_type,
        node_network_interfaces.active,
        node_network_interfaces.autostart,
        node_network_interfaces.method,
        node_network_interfaces.method6,
        node_network_interfaces.address,
        node_network_interfaces.netmask,
        node_network_interfaces.gateway,
        node_network_interfaces.cidr,
        node_network_interfaces.bridge_ports,
        node_network_interfaces.comments,
        node_network_interfaces.mtu
    ) IS DISTINCT FROM (
        EXCLUDED.iface_type,
        EXCLUDED.active,
        EXCLUDED.autostart,
        EXCLUDED.method,
        EXCLUDED.method6,
        EXCLUDED.address,
        EXCLUDED.netmask,
        EXCLUDED.gateway,
        EXCLUDED.cidr,
        EXCLUDED.bridge_ports,
        EXCLUDED.comments,
        EXCLUDED.mtu
    ) THEN now() ELSE node_network_interfaces.updated_at END,
    last_seen_at = now()
WHERE (
    node_network_interfaces.iface_type,
    node_network_interfaces.active,
    node_network_interfaces.autostart,
    node_network_interfaces.method,
    node_network_interfaces.method6,
    node_network_interfaces.address,
    node_network_interfaces.netmask,
    node_network_interfaces.gateway,
    node_network_interfaces.cidr,
    node_network_interfaces.bridge_ports,
    node_network_interfaces.comments,
    node_network_interfaces.mtu
) IS DISTINCT FROM (
    EXCLUDED.iface_type,
    EXCLUDED.active,
    EXCLUDED.autostart,
    EXCLUDED.method,
    EXCLUDED.method6,
    EXCLUDED.address,
    EXCLUDED.netmask,
    EXCLUDED.gateway,
    EXCLUDED.cidr,
    EXCLUDED.bridge_ports,
    EXCLUDED.comments,
    EXCLUDED.mtu
) OR node_network_interfaces.last_seen_at < now() - make_interval(secs => @heartbeat_seconds::int);

-- name: ListNodeNetworkInterfacesByNode :many
SELECT * FROM node_network_interfaces WHERE node_id = $1 ORDER BY iface;

-- name: DeleteStaleNodeNetworkInterfaces :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStaleVMsForNodes in vms.sql).
-- The grace window MUST exceed the upsert's @heartbeat_seconds above.
DELETE FROM node_network_interfaces
WHERE node_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);
