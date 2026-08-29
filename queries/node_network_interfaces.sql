-- name: UpsertNodeNetworkInterface :one
INSERT INTO node_network_interfaces (node_id, cluster_id, iface, iface_type, active, autostart, method, method6,
                                      address, netmask, gateway, cidr, bridge_ports, comments, mtu, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, now())
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
    -- last_seen_at marks every sync; updated_at moves only when the row's content actually
    -- changed, so it answers "when did this row last change?" rather than "when was it last polled?".
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
RETURNING *;

-- name: ListNodeNetworkInterfacesByNode :many
SELECT * FROM node_network_interfaces WHERE node_id = $1 ORDER BY iface;

-- name: DeleteStaleNodeNetworkInterfaces :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStaleVMsForNodes in vms.sql).
DELETE FROM node_network_interfaces
WHERE node_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);
