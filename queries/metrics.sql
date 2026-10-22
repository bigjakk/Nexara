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
-- Per day, the guest's disk and network rates from its counters and, from the
-- same rows rather than a second scan, its memory readings by source
-- (vm_metrics.mem_guest) with the guest's own ones summed. A reading with no
-- source (guest not running, or the row predates the column) is neither.
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
    ELSE 0 END)::double precision AS net_out_rate,
  count(*) FILTER (WHERE mem_guest)::bigint                                AS guest_samples,
  count(*) FILTER (WHERE NOT mem_guest)::bigint                            AS host_samples,
  COALESCE(sum(mem_used) FILTER (WHERE mem_guest), 0)::double precision    AS guest_mem_used,
  COALESCE(sum(mem_total) FILTER (WHERE mem_guest), 0)::double precision   AS guest_mem_total
FROM vm_metrics
WHERE vm_id = $1 AND time >= $2
GROUP BY date_trunc('day', time), vm_id
ORDER BY day;

-- name: GetVMRightsizingStats :many
-- Per guest of one cluster, its raw readings since @since: how many there are,
-- how many were taken while it ran, CPU over those, and the guest's own memory
-- readings. A reading counts as running when it records a memory source
-- (mem_guest is set only while running, since 000107) or any memory in use; a
-- stopped guest's readings are zeros. A reading is the guest's own when
-- mem_guest says so, or when it is a container's from before 000107: a
-- container's figure was always its own cgroup's, a VM's source is unknown
-- before then. Every FILTERed aggregate is COALESCEd because sqlc scans it into
-- a non-null float: the counts say whether a figure is real.
SELECT
  m.vm_id,
  count(*)::bigint AS samples,
  count(*) FILTER (WHERE m.mem_guest IS NOT NULL OR m.mem_used > 0)::bigint AS running_samples,
  min(m.time)::timestamptz AS first_sample,
  COALESCE(avg(m.cpu_usage) FILTER (WHERE m.mem_guest IS NOT NULL OR m.mem_used > 0), 0)::double precision AS cpu_avg,
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY m.cpu_usage) FILTER (WHERE m.mem_guest IS NOT NULL OR m.mem_used > 0), 0)::double precision AS cpu_p95,
  COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY m.cpu_usage) FILTER (WHERE m.mem_guest IS NOT NULL OR m.mem_used > 0), 0)::double precision AS cpu_p99,
  COALESCE(max(m.cpu_usage) FILTER (WHERE m.mem_guest IS NOT NULL OR m.mem_used > 0), 0)::double precision AS cpu_max,
  count(*) FILTER (WHERE (m.mem_guest OR (m.mem_guest IS NULL AND v.type = 'lxc' AND m.mem_used > 0)))::bigint AS guest_samples,
  count(*) FILTER (WHERE NOT m.mem_guest)::bigint AS host_samples,
  COALESCE(avg(m.mem_used::double precision / NULLIF(m.mem_total, 0)) FILTER (WHERE (m.mem_guest OR (m.mem_guest IS NULL AND v.type = 'lxc' AND m.mem_used > 0))), 0)::double precision AS guest_mem_avg_ratio,
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY m.mem_used::double precision / NULLIF(m.mem_total, 0)) FILTER (WHERE (m.mem_guest OR (m.mem_guest IS NULL AND v.type = 'lxc' AND m.mem_used > 0)) AND m.mem_total > 0), 0)::double precision AS guest_mem_p95_ratio,
  COALESCE(max(m.mem_used::double precision / NULLIF(m.mem_total, 0)) FILTER (WHERE (m.mem_guest OR (m.mem_guest IS NULL AND v.type = 'lxc' AND m.mem_used > 0))), 0)::double precision AS guest_mem_max_ratio,
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY m.mem_used) FILTER (WHERE (m.mem_guest OR (m.mem_guest IS NULL AND v.type = 'lxc' AND m.mem_used > 0))), 0)::double precision AS guest_mem_p95,
  COALESCE(max(m.mem_used) FILTER (WHERE (m.mem_guest OR (m.mem_guest IS NULL AND v.type = 'lxc' AND m.mem_used > 0))), 0)::double precision AS guest_mem_max
FROM vm_metrics m
JOIN vms v ON v.id = m.vm_id
WHERE v.cluster_id = @cluster_id AND NOT v.template AND m.time >= @since
GROUP BY m.vm_id;

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
