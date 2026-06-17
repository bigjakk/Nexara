-- name: GetLatestCephClusterMetrics :one
SELECT *
FROM ceph_cluster_metrics
WHERE cluster_id = $1
ORDER BY time DESC
LIMIT 1;

-- GetLatestCephHealthPerCluster returns the most recent Ceph health (status +
-- per-issue checks) for every cluster reporting within the freshness window, so
-- the clusters list can surface health app-wide without per-cluster live calls.
-- name: GetLatestCephHealthPerCluster :many
SELECT DISTINCT ON (cluster_id)
    cluster_id, health_status, health_checks
FROM ceph_cluster_metrics
WHERE time > now() - interval '15 minutes'
ORDER BY cluster_id, time DESC;

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
