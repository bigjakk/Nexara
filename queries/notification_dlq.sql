-- Notification Dead-Letter Queue

-- name: InsertNotificationDLQ :one
INSERT INTO notification_dlq (
    channel_id, channel_type, channel_name, alert_id, rule_id, cluster_id,
    payload, last_error, attempt_count, state, failure_kind
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetNotificationDLQ :one
SELECT * FROM notification_dlq WHERE id = $1;

-- name: ListNotificationDLQ :many
-- channel_id is optional. Pass NULL via sqlc.narg to match all channels;
-- otherwise filter to the given channel.
SELECT * FROM notification_dlq
WHERE (@state::text = '' OR state = @state::text)
  AND (sqlc.narg('channel_id')::uuid IS NULL OR channel_id = sqlc.narg('channel_id'))
ORDER BY created_at DESC
LIMIT @limit_val OFFSET @offset_val;

-- name: CountNotificationDLQByState :one
SELECT
    COUNT(*) FILTER (WHERE state = 'pending') AS pending_count,
    COUNT(*) FILTER (WHERE state = 'rate_limited') AS rate_limited_count,
    COUNT(*) FILTER (WHERE state = 'retrying') AS retrying_count,
    COUNT(*) FILTER (WHERE state = 'resolved') AS resolved_count,
    COUNT(*) FILTER (WHERE state = 'dismissed') AS dismissed_count
FROM notification_dlq;

-- name: UpdateNotificationDLQState :exec
UPDATE notification_dlq
SET state = $2, last_error = $3, attempt_count = $4, updated_at = now()
WHERE id = $1;

-- name: MarkNotificationDLQResolved :exec
UPDATE notification_dlq
SET state = 'resolved', updated_at = now()
WHERE id = $1;

-- name: DismissNotificationDLQ :exec
UPDATE notification_dlq
SET state = 'dismissed', updated_at = now()
WHERE id = $1;

-- name: DeleteNotificationDLQ :exec
DELETE FROM notification_dlq WHERE id = $1;

-- name: PurgeOldNotificationDLQ :exec
DELETE FROM notification_dlq
WHERE state IN ('resolved', 'dismissed')
  AND updated_at < now() - INTERVAL '30 days';
