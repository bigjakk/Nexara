-- name: InsertAuditLog :exec
INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, action, details)
VALUES ($1, $2, $3, $4, $5, $6);

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
  u.email AS user_email,
  u.display_name AS user_display_name,
  COALESCE(c.name, '') AS cluster_name,
  COALESCE(v.vmid, 0) AS resource_vmid,
  COALESCE(v.name, '') AS resource_name
FROM audit_log a
JOIN users u ON u.id = a.user_id
LEFT JOIN clusters c ON c.id = a.cluster_id
LEFT JOIN vms v ON v.id::text = a.resource_id
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR a.cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('resource_type')::text IS NULL OR a.resource_type = sqlc.narg('resource_type'))
ORDER BY a.created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountAuditLog :one
SELECT count(*) FROM audit_log
WHERE (sqlc.narg('cluster_id')::uuid IS NULL OR cluster_id = sqlc.narg('cluster_id'))
  AND (sqlc.narg('resource_type')::text IS NULL OR resource_type = sqlc.narg('resource_type'));
