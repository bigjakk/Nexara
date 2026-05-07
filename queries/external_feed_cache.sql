-- name: UpsertExternalFeedCache :exec
INSERT INTO external_feed_cache (source, body, content_hash, etag, fetched_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (source) DO UPDATE SET
    body = EXCLUDED.body,
    content_hash = EXCLUDED.content_hash,
    etag = EXCLUDED.etag,
    fetched_at = now();

-- name: GetExternalFeedCache :one
SELECT source, body, content_hash, etag, fetched_at
FROM external_feed_cache
WHERE source = $1;

-- name: DeleteExternalFeedCache :exec
DELETE FROM external_feed_cache WHERE source = $1;
