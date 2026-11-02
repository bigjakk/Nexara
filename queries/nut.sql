-- NUT servers: the upsd instances Nexara reads UPS data from (000131).

-- name: CreateNutServer :one
INSERT INTO nut_servers (
    name, host, port, username, password_encrypted,
    tls_mode, verify_tls, tls_fingerprint, enabled,
    nut_version, protocol_version
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetNutServer :one
SELECT * FROM nut_servers WHERE id = $1;

-- name: ListNutServers :many
SELECT * FROM nut_servers ORDER BY name;

-- name: ListEnabledNutServers :many
SELECT * FROM nut_servers WHERE enabled ORDER BY name;

-- name: UpdateNutServer :one
UPDATE nut_servers
SET name = $2,
    host = $3,
    port = $4,
    username = $5,
    password_encrypted = $6,
    tls_mode = $7,
    verify_tls = $8,
    tls_fingerprint = $9,
    enabled = $10,
    nut_version = $11,
    protocol_version = $12
WHERE id = $1
RETURNING *;

-- name: DeleteNutServer :exec
-- Devices and their events go by cascade. Their readings do not (ups_metrics
-- is a hypertable with no foreign key), so call DeleteUpsMetricsForServer
-- first, in the same transaction.
DELETE FROM nut_servers WHERE id = $1;

-- name: DeleteUpsMetricsForServer :exec
DELETE FROM ups_metrics
WHERE ups_id IN (SELECT id FROM ups_devices WHERE server_id = $1);

-- name: SetNutServerPolled :exec
UPDATE nut_servers
SET last_poll_at = now(),
    last_poll_error = '',
    nut_version = $2,
    protocol_version = $3
WHERE id = $1;

-- name: SetNutServerPollError :exec
-- Deliberately leaves last_poll_at alone: a server that has failed for an
-- hour must not read "polled 5 seconds ago" beside its error.
UPDATE nut_servers SET last_poll_error = $2 WHERE id = $1;
