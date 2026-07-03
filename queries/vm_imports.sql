-- name: InsertVMImportJob :one
INSERT INTO vm_import_jobs (
    cluster_id, source_acquisition, source_format, source_ref,
    target_node, target_storage, target_vmid, name,
    warnings_json, options_json, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetVMImportJob :one
SELECT * FROM vm_import_jobs WHERE id = $1;

-- name: ListVMImportJobsByCluster :many
SELECT * FROM vm_import_jobs
WHERE cluster_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: SetVMImportJobUPID :exec
UPDATE vm_import_jobs
SET upid = $2, updated_at = now()
WHERE id = $1;

-- name: StartVMImportJob :exec
UPDATE vm_import_jobs
SET status = 'running', started_at = now(), updated_at = now()
WHERE id = $1;

-- name: CompleteVMImportJob :exec
UPDATE vm_import_jobs
SET status = 'completed', completed_at = now(), updated_at = now()
WHERE id = $1;

-- name: FailVMImportJob :exec
UPDATE vm_import_jobs
SET status = 'failed', failure_reason = $2, completed_at = now(), updated_at = now()
WHERE id = $1;

-- name: CancelVMImportJob :exec
UPDATE vm_import_jobs
SET status = 'cancelled', completed_at = now(), updated_at = now()
WHERE id = $1 AND status IN ('pending', 'running');

-- name: ListActiveVMImportJobs :many
SELECT * FROM vm_import_jobs
WHERE status IN ('pending', 'running') AND upid <> ''
ORDER BY created_at ASC;

-- name: FailStalePendingVMImportJobs :exec
-- Fail jobs that never got a UPID: the dispatch was interrupted (process crash/restart)
-- between recording the job and starting the Proxmox create task, so they would otherwise
-- sit 'pending' with no task to reconcile against forever.
UPDATE vm_import_jobs
SET status = 'failed',
    failure_reason = 'dispatch interrupted before the import task started — check the Proxmox task history',
    completed_at = now(),
    updated_at = now()
WHERE status = 'pending' AND upid = '' AND created_at < now() - interval '30 minutes';
