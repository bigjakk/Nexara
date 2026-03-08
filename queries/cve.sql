-- name: InsertCVEScan :one
INSERT INTO cve_scans (cluster_id, status, total_nodes)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetCVEScan :one
SELECT * FROM cve_scans WHERE id = $1;

-- name: UpdateCVEScanStatus :exec
UPDATE cve_scans
SET status = $2, error_message = $3, completed_at = $4
WHERE id = $1;

-- name: UpdateCVEScanCounts :exec
UPDATE cve_scans
SET scanned_nodes = $2, total_vulns = $3,
    critical_count = $4, high_count = $5, medium_count = $6, low_count = $7
WHERE id = $1;

-- name: ListCVEScans :many
SELECT * FROM cve_scans
WHERE cluster_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: DeleteCVEScan :exec
DELETE FROM cve_scans WHERE id = $1;

-- name: InsertCVEScanNode :one
INSERT INTO cve_scan_nodes (scan_id, node_id, node_name, status)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateCVEScanNode :exec
UPDATE cve_scan_nodes
SET status = $2, packages_total = $3, vulns_found = $4,
    posture_score = $5, error_message = $6, scanned_at = $7
WHERE id = $1;

-- name: ListCVEScanNodes :many
SELECT * FROM cve_scan_nodes
WHERE scan_id = $1
ORDER BY node_name;

-- name: InsertCVEScanVuln :one
INSERT INTO cve_scan_vulns (scan_id, scan_node_id, cve_id, package_name, current_version, fixed_version, severity, cvss_score, description)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListCVEScanVulns :many
SELECT * FROM cve_scan_vulns
WHERE scan_id = $1
ORDER BY
    CASE severity
        WHEN 'critical' THEN 0
        WHEN 'high' THEN 1
        WHEN 'medium' THEN 2
        WHEN 'low' THEN 3
        ELSE 4
    END,
    package_name;

-- name: ListCVEScanVulnsBySeverity :many
SELECT * FROM cve_scan_vulns
WHERE scan_id = $1 AND severity = $2
ORDER BY package_name;

-- name: ListCVEScanVulnsByNode :many
SELECT * FROM cve_scan_vulns
WHERE scan_node_id = $1
ORDER BY
    CASE severity
        WHEN 'critical' THEN 0
        WHEN 'high' THEN 1
        WHEN 'medium' THEN 2
        WHEN 'low' THEN 3
        ELSE 4
    END,
    package_name;

-- name: UpsertCVECache :exec
INSERT INTO cve_cache (cve_id, severity, cvss_score, description, published_at, fetched_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (cve_id) DO UPDATE SET
    severity = EXCLUDED.severity,
    cvss_score = EXCLUDED.cvss_score,
    description = EXCLUDED.description,
    published_at = EXCLUDED.published_at,
    fetched_at = now();

-- name: GetCVECacheByID :one
SELECT * FROM cve_cache WHERE cve_id = $1;

-- name: GetCVECacheAge :one
SELECT MIN(fetched_at) AS oldest_fetch FROM cve_cache;

-- name: GetLatestCVEScan :one
SELECT * FROM cve_scans
WHERE cluster_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: GetCVEScanSchedule :one
SELECT * FROM cve_scan_schedules WHERE cluster_id = $1;

-- name: UpsertCVEScanSchedule :one
INSERT INTO cve_scan_schedules (cluster_id, enabled, interval_hours, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (cluster_id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    interval_hours = EXCLUDED.interval_hours,
    updated_at = now()
RETURNING *;

-- name: ListEnabledCVEScanSchedules :many
SELECT * FROM cve_scan_schedules WHERE enabled = true;

-- name: GetClusterSecuritySummary :one
SELECT
    s.id AS scan_id,
    s.status,
    s.total_vulns,
    s.critical_count,
    s.high_count,
    s.medium_count,
    s.low_count,
    s.total_nodes,
    s.scanned_nodes,
    s.started_at,
    s.completed_at
FROM cve_scans s
WHERE s.cluster_id = $1 AND s.status = 'completed'
ORDER BY s.created_at DESC
LIMIT 1;

