-- name: UpsertNodeNetworkInterface :one
INSERT INTO node_network_interfaces (node_id, cluster_id, iface, iface_type, active, autostart, method, method6,
                                      address, netmask, gateway, cidr, bridge_ports, comments, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now())
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
    last_seen_at = now()
RETURNING *;

-- name: ListNodeNetworkInterfacesByNode :many
SELECT * FROM node_network_interfaces WHERE node_id = $1 ORDER BY iface;

-- name: DeleteStaleNodeNetworkInterfaces :exec
DELETE FROM node_network_interfaces WHERE node_id = $1 AND last_seen_at < $2;
