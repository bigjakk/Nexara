-- name: GetDRSConfig :one
SELECT * FROM drs_configs WHERE cluster_id = $1;

-- name: UpsertDRSConfig :one
--
-- exclude_veeam_workers is the only field a caller may OMIT, and omitting it
-- preserves whatever is stored rather than asserting a value. It defaults to
-- TRUE, so a plain boolean would read an absent key as false and let any
-- client that predates the field disarm the protection on its next save; and
-- forcing true instead would re-arm a flag an operator had deliberately turned
-- off, from a stale browser tab saving an unrelated threshold change. Neither
-- is a decision the caller made. A new row gets the armed default.
INSERT INTO drs_configs (cluster_id, mode, enabled, weights, imbalance_threshold, eval_interval_seconds, include_containers, exclude_veeam_workers)
VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(sqlc.narg('exclude_veeam_workers')::boolean, true))
ON CONFLICT (cluster_id) DO UPDATE SET
    mode = EXCLUDED.mode,
    enabled = EXCLUDED.enabled,
    weights = EXCLUDED.weights,
    imbalance_threshold = EXCLUDED.imbalance_threshold,
    eval_interval_seconds = EXCLUDED.eval_interval_seconds,
    include_containers = EXCLUDED.include_containers,
    exclude_veeam_workers = COALESCE(sqlc.narg('exclude_veeam_workers')::boolean, drs_configs.exclude_veeam_workers),
    updated_at = now()
RETURNING *;

-- name: ListEnabledDRSConfigs :many
SELECT * FROM drs_configs WHERE enabled = true AND mode != 'disabled';

-- RequestDRSEvaluation queues an out-of-band evaluation for the scheduler
-- leader to pick up on its next tick. The API's manual-trigger endpoint uses
-- this instead of executing migrations itself, so every dispatch goes through
-- the single leader-held executor (see migration 000079).
-- name: RequestDRSEvaluation :exec
UPDATE drs_configs SET eval_requested_at = now() WHERE cluster_id = $1;

-- ClearDRSEvalRequest clears the queue slot once the scheduler has honoured
-- it. The `<= $2` guard is load-bearing: $2 is the eval_requested_at the
-- scheduler READ for this cluster, not now(). A request stamped after that
-- read — while earlier clusters in the same pass were still evaluating, or
-- while this cluster's own evaluation ran — is newer, fails the comparison,
-- and survives to be serviced by the following tick instead of being cleared
-- without ever being acted on.
-- name: ClearDRSEvalRequest :exec
UPDATE drs_configs
SET eval_requested_at = NULL
WHERE cluster_id = $1 AND eval_requested_at <= $2;

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
