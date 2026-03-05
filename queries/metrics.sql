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
