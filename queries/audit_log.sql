-- name: InsertAuditLog :exec
INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, action, details)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: InsertAuditLogWithSource :exec
INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, action, details, source, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAuditLogByCluster :many
SELECT * FROM audit_log WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: ListAuditLog :many
SELECT * FROM audit_log ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListAuditLogFiltered :many
SELECT * FROM audit_log
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('resource_type')::text IS NULL OR resource_type = sqlc.narg('resource_type'))
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListAuditLogEnriched :many
SELECT
  a.id,
  a.cluster_id,
  a.user_id,
  a.resource_type,
  a.resource_id,
  a.action,
  a.details,
  a.created_at,
  a.source,
  u.email AS user_email,
  u.display_name AS user_display_name,
  COALESCE(c.name, '') AS cluster_name,
  COALESCE(v.vmid, 0) AS resource_vmid,
  COALESCE(v.name, '') AS resource_name
FROM audit_log a
LEFT JOIN users u ON u.id = a.user_id
LEFT JOIN clusters c ON c.id = a.cluster_id
LEFT JOIN vms v ON v.id::text = a.resource_id
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR a.cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('resource_type')::text IS NULL OR a.resource_type = sqlc.narg('resource_type'))
ORDER BY a.created_at DESC
LIMIT $1 OFFSET $2;

-- ListRecentAuditLogEnriched backs the dashboard activity feed. It takes the
-- caller's view:audit scope (see the accessible_cluster_ids note on
-- ListAuditLogAdvanced) rather than trimming afterwards: LIMIT 50 applied
-- before the scope would hand a cluster-scoped user whatever survives of the
-- newest 50 global rows — usually far fewer than 50, sometimes none.
-- name: ListRecentAuditLogEnriched :many
SELECT
  a.id,
  a.cluster_id,
  a.user_id,
  a.resource_type,
  a.resource_id,
  a.action,
  a.details,
  a.created_at,
  a.source,
  u.email AS user_email,
  u.display_name AS user_display_name,
  COALESCE(c.name, '') AS cluster_name,
  COALESCE(v.vmid, 0) AS resource_vmid,
  COALESCE(v.name, '') AS resource_name,
  COALESCE(th.status, '') AS task_status,
  COALESCE(th.exit_status, '') AS task_exit_status,
  th.progress AS task_progress
FROM audit_log a
LEFT JOIN users u ON u.id = a.user_id
LEFT JOIN clusters c ON c.id = a.cluster_id
LEFT JOIN vms v ON v.id::text = a.resource_id
LEFT JOIN task_history th ON th.upid = (a.details->>'upid') AND th.cluster_id = a.cluster_id
WHERE (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR a.cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
ORDER BY a.created_at DESC
LIMIT 50;

-- name: CountAuditLog :one
SELECT count(*) FROM audit_log
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('resource_type')::text IS NULL OR resource_type = sqlc.narg('resource_type'));

-- ListAuditLogAdvanced backs the audit log page and the CSV/JSON/syslog export:
-- the optional cluster/type/user/action/source/time filters plus offset
-- pagination. accessible_cluster_ids carries the caller's view:audit RBAC
-- scope: NULL means global access (no restriction); an array restricts rows —
-- and the Total that CountAuditLogAdvanced feeds into pagination — to those
-- clusters ('{}' matches nothing).
--
-- audit_log.cluster_id is NULLABLE, unlike task_history's. A NULL cluster_id
-- marks a global entry (a settings change, a login) that only a holder of
-- global view:audit may read. `cluster_id = ANY(array)` yields NULL — not true
-- — for a NULL cluster_id, so this one clause already excludes those rows from
-- a scoped caller. Do NOT "repair" it into
-- `(a.cluster_id IS NULL OR a.cluster_id = ANY(...))`: that hands every global
-- entry to every cluster-scoped user. TestAuditScopeSQL_ExcludesNullCluster
-- pins both halves.
-- name: ListAuditLogAdvanced :many
SELECT
  a.id,
  a.cluster_id,
  a.user_id,
  a.resource_type,
  a.resource_id,
  a.action,
  a.details,
  a.created_at,
  a.source,
  u.email AS user_email,
  u.display_name AS user_display_name,
  COALESCE(c.name, '') AS cluster_name,
  COALESCE(v.vmid, 0) AS resource_vmid,
  COALESCE(v.name, '') AS resource_name
FROM audit_log a
LEFT JOIN users u ON u.id = a.user_id
LEFT JOIN clusters c ON c.id = a.cluster_id
LEFT JOIN vms v ON v.id::text = a.resource_id
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR a.cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('resource_type')::text IS NULL OR a.resource_type = sqlc.narg('resource_type'))
  AND (sqlc.narg('user_id')::uuid IS NULL OR a.user_id = sqlc.narg('user_id'))
  AND (sqlc.narg('action')::text IS NULL OR a.action = sqlc.narg('action'))
  AND (sqlc.narg('source')::text IS NULL OR a.source = sqlc.narg('source'))
  AND (sqlc.narg('start_time')::timestamptz IS NULL OR a.created_at >= sqlc.narg('start_time'))
  AND (sqlc.narg('end_time')::timestamptz IS NULL OR a.created_at <= sqlc.narg('end_time'))
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR a.cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
ORDER BY a.created_at DESC
LIMIT $1 OFFSET $2;

-- CountAuditLogAdvanced returns the total matching the same filters, for the
-- audit page pagination. Must stay filter-for-filter in sync with
-- ListAuditLogAdvanced — in particular accessible_cluster_ids, or the Total
-- leaks how many entries the caller's inaccessible clusters hold.
-- name: CountAuditLogAdvanced :one
SELECT count(*) FROM audit_log
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('resource_type')::text IS NULL OR resource_type = sqlc.narg('resource_type'))
  AND (sqlc.narg('user_id')::uuid IS NULL OR user_id = sqlc.narg('user_id'))
  AND (sqlc.narg('action')::text IS NULL OR action = sqlc.narg('action'))
  AND (sqlc.narg('source')::text IS NULL OR source = sqlc.narg('source'))
  AND (sqlc.narg('start_time')::timestamptz IS NULL OR created_at >= sqlc.narg('start_time'))
  AND (sqlc.narg('end_time')::timestamptz IS NULL OR created_at <= sqlc.narg('end_time'))
  AND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]));

-- name: ListDistinctAuditActions :many
SELECT DISTINCT action FROM audit_log ORDER BY action;

-- name: ListDistinctAuditUsers :many
SELECT DISTINCT u.id, u.email, u.display_name
FROM audit_log a
JOIN users u ON u.id = a.user_id
ORDER BY u.display_name;
