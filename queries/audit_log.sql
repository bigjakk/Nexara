-- name: InsertAuditLog :exec
INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, action, details)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListAuditLogByCluster :many
SELECT * FROM audit_log WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2;
