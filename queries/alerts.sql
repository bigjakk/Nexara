-- Alert Rules

-- name: InsertAlertRule :one
INSERT INTO alert_rules (name, description, enabled, severity, metric, operator, threshold,
    duration_seconds, scope_type, cluster_id, node_id, vm_vmid, cooldown_seconds, escalation_chain, created_by, message_template,
    ups_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
RETURNING *;

-- name: GetAlertRule :one
SELECT * FROM alert_rules WHERE id = $1;

-- ListAlertRules backs the Alerts page. accessible_cluster_ids carries the
-- caller's view:alert RBAC scope: NULL means global access (no restriction);
-- an array restricts rows to those clusters ('{}' matches nothing).
--
-- alert_rules.cluster_id is NULLABLE, and a NULL marks a GLOBAL rule that only
-- a holder of global view:alert may see — the same shape as audit_log, and the
-- same rule AlertHandler.ListRules applies per row. `cluster_id = ANY(array)`
-- yields NULL, not true, for those rows, so this clause already excludes them
-- from a scoped caller. Do NOT add an `OR cluster_id IS NULL` disjunct: that
-- hands every global rule to every cluster-scoped user.
--
-- Applied in SQL rather than after the fact because LIMIT/OFFSET run before the
-- trim: paging over every cluster's rules and filtering afterwards gives a
-- scoped caller short pages with holes in them.
-- name: ListAlertRules :many
SELECT * FROM alert_rules
WHERE (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
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
    cluster_id = $11, node_id = $12, vm_vmid = $13, cooldown_seconds = $14,
    escalation_chain = $15, message_template = $16, ups_id = $17, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteAlertRule :exec
DELETE FROM alert_rules WHERE id = $1;

-- Alert History

-- vm_vmid is the stable Proxmox guest identity (from alert_rules.vm_vmid) —
-- vm_id is the churn-prone vms.id surrogate kept for compat; see migration
-- 000077. ups_id is a per-UPS rule's UPS (alert_rules.ups_id, 000136).
-- name: InsertAlertHistory :one
INSERT INTO alert_history (rule_id, state, severity, cluster_id, node_id, vm_id,
    resource_name, metric, current_value, threshold, message, escalation_level, channel_id, vm_vmid, ups_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
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

-- ListAlertHistoryFiltered backs the Alerts history page. cluster_id is the
-- caller's optional filter; accessible_cluster_ids is their view:alert RBAC
-- scope and is not optional — see the note on ListAlertRules, including why a
-- NULL cluster_id row is excluded from a scoped caller by the clause alone.
-- hidden_metrics names the metrics whose alerts the caller may not read (the
-- UPS metrics, to a caller without view:ups); '{}' hides nothing, and NULL
-- would hide every row.
-- name: ListAlertHistoryFiltered :many
SELECT * FROM alert_history
WHERE (@state::text = '' OR state = @state::text)
  AND (@severity::text = '' OR severity = @severity::text)
  AND (sqlc.narg('cluster_id')::uuid IS NULL OR cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
  AND NOT (metric = ANY(@hidden_metrics::text[]))
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

-- name: ResolveAlert :execrows
UPDATE alert_history
SET state = 'resolved', resolved_at = now(), resolved_by = $2
WHERE id = $1 AND state IN ('firing', 'acknowledged');

-- name: AutoResolveAlert :execrows
-- An acknowledged alert is still active: it resolves when its condition
-- clears, as a firing one does.
UPDATE alert_history
SET state = 'resolved', resolved_at = now()
WHERE id = $1 AND state IN ('pending', 'firing', 'acknowledged');

-- name: DiscardPendingAlert :exec
-- A pending alert whose condition cleared before it fired notified no one.
-- Dropping it rather than resolving it keeps a passing spike from starting the
-- rule's cooldown, which runs from the latest resolved alert, and keeps a
-- flapping metric from leaving a row per blip. Only a still-pending row goes.
DELETE FROM alert_history WHERE id = $1 AND state = 'pending';

-- GetLatestAlertForRule backs the engine's dedup/transition/resolve logic.
-- The node param MUST be sqlc.narg (nullable pgtype.UUID): with a plain @
-- param sqlc generates non-nullable uuid.UUID, and pgx encodes uuid.Nil as
-- the zero UUID — never SQL NULL — so the IS NULL disjunct can never fire
-- and the lookup matches nothing (alerts then re-insert every tick and
-- never transition or auto-resolve).
--
-- The node dimension exists because cluster-scoped rules evaluate once per
-- node. There is deliberately NO vm dimension: a vm-scoped rule targets
-- exactly one guest, so rule_id alone identifies its alert stream — and
-- alert_history.vm_id is a vms-row UUID that churns with the collector,
-- which would break dedup across a churn boundary.
-- name: GetLatestAlertForRule :one
SELECT * FROM alert_history
WHERE rule_id = @rule_id
  AND (sqlc.narg('node_id')::uuid IS NULL OR node_id = sqlc.narg('node_id'))
ORDER BY created_at DESC
LIMIT 1;

-- name: CountActiveAlertsByCluster :one
SELECT
    COUNT(*) FILTER (WHERE state = 'firing') AS firing_count,
    COUNT(*) FILTER (WHERE state = 'pending') AS pending_count,
    COUNT(*) FILTER (WHERE state = 'acknowledged') AS acknowledged_count
FROM alert_history
WHERE cluster_id = $1 AND state IN ('pending', 'firing', 'acknowledged');

-- GetAlertSummary takes ListAlertHistoryFiltered's hidden_metrics, so the
-- counts above the Alerts list count only what the list can show.
-- name: GetAlertSummary :one
SELECT
    COUNT(*) FILTER (WHERE state = 'firing') AS firing_count,
    COUNT(*) FILTER (WHERE state = 'pending') AS pending_count,
    COUNT(*) FILTER (WHERE state = 'acknowledged') AS acknowledged_count,
    COUNT(*) FILTER (WHERE state = 'firing' AND severity = 'critical') AS critical_firing,
    COUNT(*) FILTER (WHERE state = 'firing' AND severity = 'warning') AS warning_firing,
    COUNT(*) FILTER (WHERE state = 'firing' AND severity = 'info') AS info_firing
FROM alert_history
WHERE state IN ('pending', 'firing', 'acknowledged')
  AND NOT (metric = ANY(@hidden_metrics::text[]));

-- name: UpdateAlertEscalation :exec
UPDATE alert_history
SET escalation_level = $2, channel_id = $3
WHERE id = $1;

-- Notification Channels

-- name: InsertNotificationChannel :one
INSERT INTO notification_channels (name, channel_type, config_encrypted, enabled, created_by, allow_private_address)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels WHERE id = $1;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels
ORDER BY created_at DESC;

-- name: UpdateNotificationChannel :one
UPDATE notification_channels
SET name = $2, channel_type = $3, config_encrypted = $4, enabled = $5, allow_private_address = $6,
    cgnat_grandfathered = $7, updated_at = now()
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

-- A UPS's window (000137) names the UPS and no cluster or node.
-- name: InsertUpsMaintenanceWindow :one
INSERT INTO maintenance_windows (ups_id, description, starts_at, ends_at, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListUpsMaintenanceWindows :many
SELECT * FROM maintenance_windows
WHERE ups_id = $1
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

-- name: GetNotificationChannelEnabled :one
SELECT * FROM notification_channels WHERE id = $1 AND enabled = true;

-- name: MarkAlertNotificationSent :exec
UPDATE alert_history SET notification_sent_at = now() WHERE id = $1;

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
    net_out,
    disk_unreported
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
    net_out,
    mem_guest,
    disk_unreported
FROM vm_metrics
WHERE vm_id = $1 AND time >= $2
ORDER BY time DESC;


-- ---------------------------------------------------------------------------
-- Reports.
-- ---------------------------------------------------------------------------

-- CountAlertHistoryBySeverityInWindow feeds the cluster digest: how many
-- alerts fired in the period per severity, and how many of them are resolved
-- or still open. Windowed on created_at (when the alert was raised).
-- name: CountAlertHistoryBySeverityInWindow :many
SELECT severity,
       count(*)::bigint AS fired,
       (count(*) FILTER (WHERE state = 'resolved'))::bigint AS resolved,
       (count(*) FILTER (WHERE state IN ('pending', 'firing', 'acknowledged')))::bigint AS open_count
FROM alert_history
WHERE cluster_id = @cluster_id::uuid
  AND created_at >= @since::timestamptz
  AND created_at < @until::timestamptz
GROUP BY severity
ORDER BY severity;

-- ListTopAlertRulesInWindow lists the rules that fired most in the period,
-- for the digest's "noisiest rules" table.
-- name: ListTopAlertRulesInWindow :many
SELECT r.name, ah.metric, ah.severity, count(*)::bigint AS fired
FROM alert_history ah
JOIN alert_rules r ON r.id = ah.rule_id
WHERE ah.cluster_id = @cluster_id::uuid
  AND ah.created_at >= @since::timestamptz
  AND ah.created_at < @until::timestamptz
GROUP BY r.name, ah.metric, ah.severity
ORDER BY fired DESC, r.name
LIMIT @row_limit::int;

-- name: ListActiveAlertsForNodes :many
-- The active alerts on node rows the collector is about to remove
-- (Syncer.pruneRemovedNodes), read before the delete nulls their node_id.
SELECT * FROM alert_history
WHERE node_id = ANY(@node_ids::uuid[]) AND state IN ('pending', 'firing', 'acknowledged');

-- name: DetachNodeAlertRules :one
-- Turns off the node-scoped rules of a node row the collector is about to
-- remove (with no node left they can evaluate nothing, and an edit that keeps
-- one enabled is refused for its unknown node_id), and keeps the node's name and
-- whether each rule was on, for ReattachNodeAlertRules. A rule already waiting
-- for this name keeps its flag, so a removal retried after a failed delete does
-- not forget it. Answers how many of the rules were on.
WITH detached AS (
    UPDATE alert_rules
    SET detached_node_name = @node_name,
        detached_enabled = enabled OR (detached_node_name = @node_name AND detached_enabled),
        enabled = false,
        updated_at = now()
    WHERE scope_type = 'node' AND node_id = @node_id
    RETURNING detached_enabled
)
SELECT count(*) FILTER (WHERE detached_enabled) FROM detached;

-- name: ReattachNodeAlertRules :execrows
-- Points the rules waiting for a removed node's name (DetachNodeAlertRules) at
-- the cluster's node row of that name, turning back on the ones that were on.
-- Only a rule whose node_id still names that row (the removal failed) or no row
-- at all: one an operator pointed at another node meanwhile stays there.
UPDATE alert_rules r
SET node_id = n.id,
    enabled = r.enabled OR r.detached_enabled,
    detached_node_name = '',
    detached_enabled = false,
    updated_at = now()
FROM nodes n
WHERE n.cluster_id = @cluster_id
  AND r.cluster_id = n.cluster_id
  AND r.scope_type = 'node'
  AND r.detached_node_name = n.name
  AND (r.node_id = n.id OR NOT EXISTS (SELECT 1 FROM nodes x WHERE x.id = r.node_id));

-- name: DetachNodeMaintenanceWindows :execrows
-- Keeps the node's name on the maintenance windows pinned to a node row the
-- collector is about to remove, for ReattachNodeMaintenanceWindows. A window
-- whose node_id names no row covers nothing (isInMaintenanceWindow).
UPDATE maintenance_windows SET detached_node_name = @node_name
WHERE node_id = @node_id;

-- name: ReattachNodeMaintenanceWindows :execrows
-- ReattachNodeAlertRules for maintenance windows.
UPDATE maintenance_windows w
SET node_id = n.id, detached_node_name = ''
FROM nodes n
WHERE n.cluster_id = @cluster_id
  AND w.cluster_id = n.cluster_id
  AND w.detached_node_name = n.name
  AND (w.node_id = n.id OR NOT EXISTS (SELECT 1 FROM nodes x WHERE x.id = w.node_id));
