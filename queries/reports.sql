-- Report Schedules

-- name: InsertReportSchedule :one
INSERT INTO report_schedules (name, report_type, cluster_id, time_range_hours, schedule,
    format, email_enabled, email_channel_id, email_recipients, parameters, enabled, next_run_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: GetReportSchedule :one
SELECT * FROM report_schedules WHERE id = $1;

-- ListReportSchedules backs the Reports page. accessible_cluster_ids carries
-- the caller's view:report RBAC scope: NULL means global access (no
-- restriction); an array restricts rows ('{}' matches nothing).
-- report_schedules.cluster_id is NOT NULL, so every row belongs to exactly one
-- cluster and there is no global-entry case.
--
-- Applied in SQL rather than after the fetch because the handler's cap runs
-- first: taking 100 rows across every cluster and trimming afterwards silently
-- hides a scoped caller's schedules on an install with more than 100 of them,
-- with nothing in the response marking the result incomplete.
-- name: ListReportSchedules :many
SELECT * FROM report_schedules
WHERE (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListReportSchedulesByCluster :many
SELECT * FROM report_schedules
WHERE cluster_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdateReportSchedule :one
UPDATE report_schedules
SET name = $2, report_type = $3, cluster_id = $4, time_range_hours = $5, schedule = $6,
    format = $7, email_enabled = $8, email_channel_id = $9, email_recipients = $10,
    parameters = $11, enabled = $12, next_run_at = $13, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteReportSchedule :exec
DELETE FROM report_schedules WHERE id = $1;

-- name: ListDueReportSchedules :many
SELECT * FROM report_schedules
WHERE enabled = true AND schedule != '' AND next_run_at <= now()
ORDER BY next_run_at;

-- name: UpdateReportScheduleLastRun :exec
UPDATE report_schedules
SET last_run_at = $2, next_run_at = $3, updated_at = now()
WHERE id = $1;

-- Report Runs

-- name: InsertReportRun :one
INSERT INTO report_runs (schedule_id, report_type, cluster_id, status, time_range_hours, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetReportRun :one
SELECT * FROM report_runs WHERE id = $1;

-- ListReportRuns backs the Reports run history, and takes the caller's
-- view:report scope for the same reason as ListReportSchedules above: the cap
-- is applied before the trim, so an unscoped fetch quietly drops a scoped
-- caller's runs once the install has more runs than the cap.
-- name: ListReportRuns :many
SELECT * FROM report_runs
WHERE (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL
       OR cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListReportRunsByCluster :many
SELECT * FROM report_runs
WHERE cluster_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListReportRunsBySchedule :many
SELECT * FROM report_runs
WHERE schedule_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdateReportRunStarted :exec
UPDATE report_runs SET status = 'running', started_at = now() WHERE id = $1;

-- name: UpdateReportRunCompleted :exec
UPDATE report_runs
SET status = 'completed', report_data = $2, report_html = $3, report_csv = $4, completed_at = now()
WHERE id = $1;

-- name: UpdateReportRunFailed :exec
UPDATE report_runs
SET status = 'failed', error_message = $2, completed_at = now()
WHERE id = $1;

-- name: DeleteReportRun :exec
DELETE FROM report_runs WHERE id = $1;

-- name: CleanupOldReportRuns :exec
DELETE FROM report_runs
WHERE created_at < now() - interval '90 days'
  AND schedule_id IS NOT NULL;

-- name: GetReportRunHTML :one
SELECT id, cluster_id, report_html FROM report_runs WHERE id = $1;

-- name: GetReportRunCSV :one
SELECT id, cluster_id, report_csv FROM report_runs WHERE id = $1;
