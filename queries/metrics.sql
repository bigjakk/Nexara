-- name: MarkNodeOffline :exec
UPDATE nodes SET status = 'offline' WHERE id = $1 AND status != 'offline';

-- name: MarkNodeOnline :exec
UPDATE nodes SET status = 'online' WHERE id = $1 AND status = 'offline';

-- name: GetClusterMetrics5m :many
SELECT
  m.bucket::timestamptz                   AS bucket,
  avg(m.avg_cpu_usage)::double precision  AS cpu,
  avg(m.avg_mem_used)::double precision   AS mem_used,
  avg(m.avg_mem_total)::double precision  AS mem_total,
  avg(m.avg_disk_read)::double precision  AS disk_read,
  avg(m.avg_disk_write)::double precision AS disk_write,
  avg(m.avg_net_in)::double precision     AS net_in,
  avg(m.avg_net_out)::double precision    AS net_out
FROM node_metrics_5m m
JOIN nodes n ON n.id = m.node_id
WHERE n.cluster_id = $1 AND m.bucket >= $2
GROUP BY m.bucket
ORDER BY m.bucket;

-- name: GetVMMetrics5m :many
SELECT
  m.bucket::timestamptz                   AS bucket,
  m.avg_cpu_usage::double precision       AS cpu,
  m.avg_mem_used::double precision        AS mem_used,
  m.avg_mem_total::double precision       AS mem_total,
  m.avg_disk_read::double precision       AS disk_read,
  m.avg_disk_write::double precision      AS disk_write,
  m.avg_net_in::double precision          AS net_in,
  m.avg_net_out::double precision         AS net_out
FROM vm_metrics_5m m
WHERE m.vm_id = $1 AND m.bucket >= $2
ORDER BY m.bucket;

-- name: GetVMMetrics1h :many
SELECT
  m.bucket::timestamptz                   AS bucket,
  m.avg_cpu_usage::double precision       AS cpu,
  m.avg_mem_used::double precision        AS mem_used,
  m.avg_mem_total::double precision       AS mem_total,
  m.avg_disk_read::double precision       AS disk_read,
  m.avg_disk_write::double precision      AS disk_write,
  m.avg_net_in::double precision          AS net_in,
  m.avg_net_out::double precision         AS net_out
FROM vm_metrics_1h m
WHERE m.vm_id = $1 AND m.bucket >= $2
ORDER BY m.bucket;

-- name: GetVMMetricsDailyAvg :many
SELECT
  date_trunc('day', m.bucket)::timestamptz AS day,
  m.vm_id,
  avg(m.avg_cpu_usage)::double precision   AS cpu,
  max(m.max_cpu_usage)::double precision   AS cpu_max,
  avg(m.avg_mem_used)::double precision    AS mem_used,
  max(m.max_mem_used)::double precision    AS mem_used_max,
  avg(m.avg_mem_total)::double precision   AS mem_total
FROM vm_metrics_1h m
WHERE m.vm_id = $1 AND m.bucket >= $2
GROUP BY date_trunc('day', m.bucket), m.vm_id
ORDER BY day;

-- name: GetVMIODailyRate :many
SELECT
  date_trunc('day', time)::timestamptz AS day,
  vm_id,
  (CASE WHEN EXTRACT(EPOCH FROM max(time) - min(time)) > 0
    THEN GREATEST(max(disk_read) - min(disk_read), 0)::double precision / EXTRACT(EPOCH FROM max(time) - min(time))
    ELSE 0 END)::double precision AS disk_read_rate,
  (CASE WHEN EXTRACT(EPOCH FROM max(time) - min(time)) > 0
    THEN GREATEST(max(disk_write) - min(disk_write), 0)::double precision / EXTRACT(EPOCH FROM max(time) - min(time))
    ELSE 0 END)::double precision AS disk_write_rate,
  (CASE WHEN EXTRACT(EPOCH FROM max(time) - min(time)) > 0
    THEN GREATEST(max(net_in) - min(net_in), 0)::double precision / EXTRACT(EPOCH FROM max(time) - min(time))
    ELSE 0 END)::double precision AS net_in_rate,
  (CASE WHEN EXTRACT(EPOCH FROM max(time) - min(time)) > 0
    THEN GREATEST(max(net_out) - min(net_out), 0)::double precision / EXTRACT(EPOCH FROM max(time) - min(time))
    ELSE 0 END)::double precision AS net_out_rate
FROM vm_metrics
WHERE vm_id = $1 AND time >= $2
GROUP BY date_trunc('day', time), vm_id
ORDER BY day;

-- name: GetNodeMetrics5m :many
SELECT
  m.bucket::timestamptz                   AS bucket,
  m.avg_cpu_usage::double precision       AS cpu,
  m.avg_mem_used::double precision        AS mem_used,
  m.avg_mem_total::double precision       AS mem_total,
  m.avg_disk_read::double precision       AS disk_read,
  m.avg_disk_write::double precision      AS disk_write,
  m.avg_net_in::double precision          AS net_in,
  m.avg_net_out::double precision         AS net_out
FROM node_metrics_5m m
WHERE m.node_id = $1 AND m.bucket >= $2
ORDER BY m.bucket;

-- name: GetNodeMetrics1h :many
SELECT
  m.bucket::timestamptz                   AS bucket,
  m.avg_cpu_usage::double precision       AS cpu,
  m.avg_mem_used::double precision        AS mem_used,
  m.avg_mem_total::double precision       AS mem_total,
  m.avg_disk_read::double precision       AS disk_read,
  m.avg_disk_write::double precision      AS disk_write,
  m.avg_net_in::double precision          AS net_in,
  m.avg_net_out::double precision         AS net_out
FROM node_metrics_1h m
WHERE m.node_id = $1 AND m.bucket >= $2
ORDER BY m.bucket;

-- name: GetNodeMetricsDailyAvg :many
SELECT
  date_trunc('day', m.bucket)::timestamptz AS day,
  m.node_id,
  avg(m.avg_cpu_usage)::double precision   AS cpu,
  max(m.max_cpu_usage)::double precision   AS cpu_max,
  avg(m.avg_mem_used)::double precision    AS mem_used,
  max(m.max_mem_used)::double precision    AS mem_used_max,
  avg(m.avg_mem_total)::double precision   AS mem_total,
  avg(m.avg_disk_read)::double precision   AS disk_read,
  avg(m.avg_disk_write)::double precision  AS disk_write,
  avg(m.avg_net_in)::double precision      AS net_in,
  avg(m.avg_net_out)::double precision     AS net_out
FROM node_metrics_1h m
WHERE m.node_id = $1 AND m.bucket >= $2
GROUP BY date_trunc('day', m.bucket), m.node_id
ORDER BY day;

-- name: GetNodeIODailyRate :many
SELECT
  date_trunc('day', time)::timestamptz AS day,
  node_id,
  (CASE WHEN EXTRACT(EPOCH FROM max(time) - min(time)) > 0
    THEN GREATEST(max(disk_read) - min(disk_read), 0)::double precision / EXTRACT(EPOCH FROM max(time) - min(time))
    ELSE 0 END)::double precision AS disk_read_rate,
  (CASE WHEN EXTRACT(EPOCH FROM max(time) - min(time)) > 0
    THEN GREATEST(max(disk_write) - min(disk_write), 0)::double precision / EXTRACT(EPOCH FROM max(time) - min(time))
    ELSE 0 END)::double precision AS disk_write_rate,
  (CASE WHEN EXTRACT(EPOCH FROM max(time) - min(time)) > 0
    THEN GREATEST(max(net_in) - min(net_in), 0)::double precision / EXTRACT(EPOCH FROM max(time) - min(time))
    ELSE 0 END)::double precision AS net_in_rate,
  (CASE WHEN EXTRACT(EPOCH FROM max(time) - min(time)) > 0
    THEN GREATEST(max(net_out) - min(net_out), 0)::double precision / EXTRACT(EPOCH FROM max(time) - min(time))
    ELSE 0 END)::double precision AS net_out_rate
FROM node_metrics
WHERE node_id = $1 AND time >= $2
GROUP BY date_trunc('day', time), node_id
ORDER BY day;

-- name: GetClusterMetricsDailyAvg :many
SELECT
  date_trunc('day', m.bucket)::timestamptz AS day,
  avg(m.avg_cpu_usage)::double precision   AS cpu,
  max(m.max_cpu_usage)::double precision   AS cpu_max,
  avg(m.avg_mem_used)::double precision    AS mem_used,
  max(m.max_mem_used)::double precision    AS mem_used_max,
  avg(m.avg_mem_total)::double precision   AS mem_total,
  avg(m.avg_disk_read)::double precision   AS disk_read,
  avg(m.avg_disk_write)::double precision  AS disk_write,
  avg(m.avg_net_in)::double precision      AS net_in,
  avg(m.avg_net_out)::double precision     AS net_out
FROM node_metrics_1h m
JOIN nodes n ON n.id = m.node_id
WHERE n.cluster_id = $1 AND m.bucket >= $2
GROUP BY date_trunc('day', m.bucket)
ORDER BY day;

-- name: GetClusterMetrics1h :many
SELECT
  m.bucket::timestamptz                   AS bucket,
  avg(m.avg_cpu_usage)::double precision  AS cpu,
  avg(m.avg_mem_used)::double precision   AS mem_used,
  avg(m.avg_mem_total)::double precision  AS mem_total,
  avg(m.avg_disk_read)::double precision  AS disk_read,
  avg(m.avg_disk_write)::double precision AS disk_write,
  avg(m.avg_net_in)::double precision     AS net_in,
  avg(m.avg_net_out)::double precision    AS net_out
FROM node_metrics_1h m
JOIN nodes n ON n.id = m.node_id
WHERE n.cluster_id = $1 AND m.bucket >= $2
GROUP BY m.bucket
ORDER BY m.bucket;
