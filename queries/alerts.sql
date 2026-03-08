-- Alert Rules

-- name: InsertAlertRule :one
INSERT INTO alert_rules (name, description, enabled, severity, metric, operator, threshold,
    duration_seconds, scope_type, cluster_id, node_id, vm_id, cooldown_seconds, escalation_chain, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: GetAlertRule :one
SELECT * FROM alert_rules WHERE id = $1;

-- name: ListAlertRules :many
SELECT * FROM alert_rules
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListAlertRulesByCluster :many
SELECT * FROM alert_rules
WHERE cluster_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListEnabledAlertRules :many
SELECT * FROM alert_rules WHERE enabled = true;

-- name: UpdateAlertRule :one
UPDATE alert_rules
SET name = $2, description = $3, enabled = $4, severity = $5, metric = $6,
    operator = $7, threshold = $8, duration_seconds = $9, scope_type = $10,
    cluster_id = $11, node_id = $12, vm_id = $13, cooldown_seconds = $14,
    escalation_chain = $15, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteAlertRule :exec
DELETE FROM alert_rules WHERE id = $1;

-- Alert History

-- name: InsertAlertHistory :one
INSERT INTO alert_history (rule_id, state, severity, cluster_id, node_id, vm_id,
    resource_name, metric, current_value, threshold, message, escalation_level, channel_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: GetAlertHistory :one
SELECT * FROM alert_history WHERE id = $1;

-- name: ListAlertHistory :many
SELECT * FROM alert_history
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListAlertHistoryByCluster :many
SELECT * FROM alert_history
WHERE cluster_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAlertHistoryFiltered :many
SELECT * FROM alert_history
WHERE (@state::text = '' OR state = @state::text)
  AND (@severity::text = '' OR severity = @severity::text)
  AND (@cluster_id::uuid IS NULL OR cluster_id = @cluster_id)
ORDER BY created_at DESC
LIMIT @limit_val OFFSET @offset_val;

-- name: ListActiveAlerts :many
SELECT * FROM alert_history
WHERE state IN ('pending', 'firing')
ORDER BY created_at DESC;

-- name: ListActiveAlertsByCluster :many
SELECT * FROM alert_history
WHERE state IN ('pending', 'firing') AND cluster_id = $1
ORDER BY created_at DESC;

-- name: ListFiringUnacknowledged :many
SELECT * FROM alert_history
WHERE state = 'firing' AND acknowledged_at IS NULL
ORDER BY fired_at ASC;

-- name: UpdateAlertState :exec
UPDATE alert_history SET state = $2 WHERE id = $1;

-- name: TransitionAlertToFiring :exec
UPDATE alert_history
SET state = 'firing', fired_at = now()
WHERE id = $1 AND state = 'pending';

-- name: AcknowledgeAlert :exec
UPDATE alert_history
SET state = 'acknowledged', acknowledged_at = now(), acknowledged_by = $2
WHERE id = $1 AND state = 'firing';

-- name: ResolveAlert :exec
UPDATE alert_history
SET state = 'resolved', resolved_at = now(), resolved_by = $2
WHERE id = $1 AND state IN ('firing', 'acknowledged');

-- name: AutoResolveAlert :exec
UPDATE alert_history
SET state = 'resolved', resolved_at = now()
WHERE id = $1 AND state IN ('pending', 'firing');

-- name: GetLatestAlertForRule :one
SELECT * FROM alert_history
WHERE rule_id = @rule_id
  AND (@node_id::uuid IS NULL OR node_id = @node_id)
  AND (@vm_id::uuid IS NULL OR vm_id = @vm_id)
ORDER BY created_at DESC
LIMIT 1;

-- name: CountActiveAlertsByCluster :one
SELECT
    COUNT(*) FILTER (WHERE state = 'firing') AS firing_count,
    COUNT(*) FILTER (WHERE state = 'pending') AS pending_count,
    COUNT(*) FILTER (WHERE state = 'acknowledged') AS acknowledged_count
FROM alert_history
WHERE cluster_id = $1 AND state IN ('pending', 'firing', 'acknowledged');

-- name: GetAlertSummary :one
SELECT
    COUNT(*) FILTER (WHERE state = 'firing') AS firing_count,
    COUNT(*) FILTER (WHERE state = 'pending') AS pending_count,
    COUNT(*) FILTER (WHERE state = 'acknowledged') AS acknowledged_count,
    COUNT(*) FILTER (WHERE state = 'firing' AND severity = 'critical') AS critical_firing,
    COUNT(*) FILTER (WHERE state = 'firing' AND severity = 'warning') AS warning_firing,
    COUNT(*) FILTER (WHERE state = 'firing' AND severity = 'info') AS info_firing
FROM alert_history
WHERE state IN ('pending', 'firing', 'acknowledged');

-- name: UpdateAlertEscalation :exec
UPDATE alert_history
SET escalation_level = $2, channel_id = $3
WHERE id = $1;

-- Notification Channels

-- name: InsertNotificationChannel :one
INSERT INTO notification_channels (name, channel_type, config_encrypted, enabled, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels WHERE id = $1;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels
ORDER BY created_at DESC;

-- name: UpdateNotificationChannel :one
UPDATE notification_channels
SET name = $2, channel_type = $3, config_encrypted = $4, enabled = $5, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteNotificationChannel :exec
DELETE FROM notification_channels WHERE id = $1;

-- Maintenance Windows

-- name: InsertMaintenanceWindow :one
INSERT INTO maintenance_windows (cluster_id, node_id, description, starts_at, ends_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetMaintenanceWindow :one
SELECT * FROM maintenance_windows WHERE id = $1;

-- name: ListMaintenanceWindows :many
SELECT * FROM maintenance_windows
WHERE cluster_id = $1
ORDER BY starts_at DESC
LIMIT $2 OFFSET $3;

-- name: ListActiveMaintenanceWindows :many
SELECT * FROM maintenance_windows
WHERE now() BETWEEN starts_at AND ends_at;

-- name: UpdateMaintenanceWindow :one
UPDATE maintenance_windows
SET description = $2, starts_at = $3, ends_at = $4, node_id = $5, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteMaintenanceWindow :exec
DELETE FROM maintenance_windows WHERE id = $1;

-- Metric queries for alert evaluation

-- name: GetNodeRecentMetrics :many
SELECT
    time AS bucket,
    cpu_usage,
    mem_used,
    mem_total,
    disk_read,
    disk_write,
    net_in,
    net_out
FROM node_metrics
WHERE node_id = $1 AND time >= $2
ORDER BY time DESC;

-- name: GetVMRecentMetrics :many
SELECT
    time AS bucket,
    cpu_usage,
    mem_used,
    mem_total,
    disk_read,
    disk_write,
    net_in,
    net_out
FROM vm_metrics
WHERE vm_id = $1 AND time >= $2
ORDER BY time DESC;

