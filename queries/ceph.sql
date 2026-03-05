-- name: GetLatestCephClusterMetrics :one
SELECT *
FROM ceph_cluster_metrics
WHERE cluster_id = $1
ORDER BY time DESC
LIMIT 1;

-- name: GetCephClusterMetricsHistory :many
SELECT *
FROM ceph_cluster_metrics
WHERE cluster_id = $1
  AND time >= $2
  AND time <= $3
ORDER BY time ASC;

-- name: GetCephClusterMetrics5m :many
SELECT *
FROM ceph_cluster_metrics_5m
WHERE cluster_id = $1
  AND bucket >= $2
  AND bucket <= $3
ORDER BY bucket ASC;

-- name: GetCephClusterMetrics1h :many
SELECT *
FROM ceph_cluster_metrics_1h
WHERE cluster_id = $1
  AND bucket >= $2
  AND bucket <= $3
ORDER BY bucket ASC;

-- name: GetLatestCephOSDMetrics :many
SELECT DISTINCT ON (osd_id) *
FROM ceph_osd_metrics
WHERE cluster_id = $1
ORDER BY osd_id, time DESC;

-- name: GetLatestCephPoolMetrics :many
SELECT DISTINCT ON (pool_id) *
FROM ceph_pool_metrics
WHERE cluster_id = $1
ORDER BY pool_id, time DESC;
