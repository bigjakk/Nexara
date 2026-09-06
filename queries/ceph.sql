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
-- Downsampled in the database, deliberately. The raw hypertable holds one row
-- per cluster per METRICS_COLLECT_INTERVAL — 10s in docker-compose.yml and
-- .env.example — so a 7-day window runs to tens of thousands of rows. That is
-- megabytes of JSON the browser parses, formats and lays out on its only
-- thread, and CephMetricsChart draws it into four charts on a 60s refetch. The
-- caller derives bucket_seconds from the timeframe so every window comes back
-- at chart resolution instead.
--
-- The bucket is aliased `bucket`, matching the ceph_cluster_metrics_5m/_1h
-- continuous aggregates in migration 000006. That alias is doing real work:
-- naming it `time` would make `GROUP BY time` ambiguous, and PostgreSQL
-- resolves an ambiguous GROUP BY name to the INPUT column — silently grouping
-- per raw sample and restoring the full response with no error. No column is
-- called `bucket`, so the grouping can only mean the expression. The API's
-- JSON key stays "time": cephClusterMetricResponse maps it.
--
-- Reducers are chosen per column, not uniformly:
--   * osds_up / osds_in use MIN, not AVG. These feed the "OSDs Up" chart,
--     which exists to show an operator when OSDs dropped out. Averaging hides
--     exactly that: one OSD down for five minutes inside a ten-minute bucket
--     rounds straight back to the full count, so the dip disappears at every
--     timeframe. MIN keeps "something went down in this window" visible.
--     This deliberately diverges from the _5m/_1h aggregates above, which are
--     generic rollups rather than an availability signal.
--   * The remaining gauges average, which is the faithful reducer for them.
--   * health_status takes the bucket's last value, matching those aggregates.
--
-- health_checks is not selected. The history DTO already drops it (see
-- cephClusterMetricResponse) because only the latest health needs reasons, so
-- aggregating a per-sample JSONB here would cost work nothing consumes.
--
-- No ORDER BY tiebreaker is needed, unlike the PBS equivalent: cluster_id is
-- pinned by the WHERE clause, so each bucket yields exactly one row.
SELECT
    time_bucket(make_interval(secs => @bucket_seconds::int), time)::timestamptz AS bucket,
    cluster_id,
    last(health_status, time)::text     AS health_status,
    AVG(osds_total)::int                AS osds_total,
    MIN(osds_up)::int                   AS osds_up,
    MIN(osds_in)::int                   AS osds_in,
    AVG(pgs_total)::int                 AS pgs_total,
    AVG(bytes_used)::bigint             AS bytes_used,
    AVG(bytes_avail)::bigint            AS bytes_avail,
    AVG(bytes_total)::bigint            AS bytes_total,
    AVG(read_ops_sec)::bigint           AS read_ops_sec,
    AVG(write_ops_sec)::bigint          AS write_ops_sec,
    AVG(read_bytes_sec)::bigint         AS read_bytes_sec,
    AVG(write_bytes_sec)::bigint        AS write_bytes_sec
FROM ceph_cluster_metrics
WHERE cluster_id = @cluster_id
  AND time >= @start_time
  AND time <= @end_time
GROUP BY bucket, cluster_id
ORDER BY bucket ASC;

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
