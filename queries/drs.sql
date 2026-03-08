-- name: GetDRSConfig :one
SELECT * FROM drs_configs WHERE cluster_id = $1;

-- name: UpsertDRSConfig :one
INSERT INTO drs_configs (cluster_id, mode, enabled, weights, imbalance_threshold, eval_interval_seconds)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (cluster_id) DO UPDATE SET
    mode = EXCLUDED.mode,
    enabled = EXCLUDED.enabled,
    weights = EXCLUDED.weights,
    imbalance_threshold = EXCLUDED.imbalance_threshold,
    eval_interval_seconds = EXCLUDED.eval_interval_seconds,
    updated_at = now()
RETURNING *;

-- name: ListEnabledDRSConfigs :many
SELECT * FROM drs_configs WHERE enabled = true AND mode != 'disabled';

-- name: ListDRSRules :many
SELECT * FROM drs_rules WHERE cluster_id = $1 ORDER BY created_at;

-- name: GetDRSRule :one
SELECT * FROM drs_rules WHERE id = $1;

-- name: InsertDRSRule :one
INSERT INTO drs_rules (cluster_id, rule_type, vm_ids, node_names, enabled)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateDRSRule :exec
UPDATE drs_rules
SET rule_type = $2, vm_ids = $3, node_names = $4, enabled = $5, updated_at = now()
WHERE id = $1;

-- name: DeleteDRSRule :exec
DELETE FROM drs_rules WHERE id = $1;

-- name: InsertDRSHistory :one
INSERT INTO drs_history (cluster_id, source_node, target_node, vm_id, vm_type, reason, score_before, score_after, status, executed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: ListDRSHistory :many
SELECT * FROM drs_history
WHERE cluster_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: UpdateDRSHistoryStatus :exec
UPDATE drs_history SET status = $2, executed_at = $3 WHERE id = $1;

-- name: CleanupStaleDRSHistory :exec
UPDATE drs_history
SET status = 'cancelled', executed_at = now()
WHERE status = 'pending' AND created_at < now() - interval '60 minutes';

-- name: SetDRSEnabled :exec
UPDATE drs_configs SET enabled = $2, updated_at = now() WHERE cluster_id = $1;

-- name: GetLastDRSMigrationForVM :one
SELECT * FROM drs_history
WHERE cluster_id = $1 AND vm_id = $2 AND status IN ('completed', 'pending')
ORDER BY created_at DESC
LIMIT 1;
