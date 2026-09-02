-- name: GetGuestToolsConfig :one
SELECT * FROM guest_tools_configs WHERE cluster_id = $1;

-- name: UpsertGuestToolsConfig :one
--
-- snapshot_before follows the omit-vs-assert idiom from UpsertDRSConfig. It is
-- the rollback for a driver swap that can leave a guest unbootable, so an
-- absent key preserves the stored value rather than reading as false: a client
-- that predates the field must not be able to disarm it, and a stale browser
-- tab saving an unrelated change must not either.
INSERT INTO guest_tools_configs (cluster_id, mode, target_version, snapshot_before, max_concurrent)
VALUES ($1, $2, $3, COALESCE(sqlc.narg('snapshot_before')::boolean, false), $4)
ON CONFLICT (cluster_id) DO UPDATE SET
    mode            = EXCLUDED.mode,
    target_version  = EXCLUDED.target_version,
    max_concurrent  = EXCLUDED.max_concurrent,
    snapshot_before = COALESCE(sqlc.narg('snapshot_before')::boolean, guest_tools_configs.snapshot_before)
RETURNING *;

-- name: ListActiveGuestToolsConfigs :many
SELECT * FROM guest_tools_configs WHERE mode <> 'disabled';

-- name: GetGuestToolsPolicy :one
SELECT * FROM guest_tools_policies WHERE cluster_id = $1 AND vmid = $2;

-- name: ListGuestToolsPolicies :many
SELECT * FROM guest_tools_policies WHERE cluster_id = $1 ORDER BY vmid;

-- name: UpsertGuestToolsPolicy :one
--
-- excluded uses the same omit-vs-assert guard, for the same reason in reverse:
-- an exclusion is the operator saying "never touch this guest", and no client
-- that simply does not know about the field should be able to clear it.
INSERT INTO guest_tools_policies (cluster_id, vmid, excluded, target_version, note)
VALUES ($1, $2, COALESCE(sqlc.narg('excluded')::boolean, false), $3, $4)
ON CONFLICT (cluster_id, vmid) DO UPDATE SET
    excluded       = COALESCE(sqlc.narg('excluded')::boolean, guest_tools_policies.excluded),
    target_version = EXCLUDED.target_version,
    note           = EXCLUDED.note
RETURNING *;

-- name: GetGuestToolsState :one
SELECT * FROM guest_tools_state WHERE cluster_id = $1 AND vmid = $2;

-- ListGuestToolsFleet is the cluster-wide view: every Windows guest Nexara
-- knows about, with whatever has been observed and whatever policy applies.
--
-- Driven from vms rather than from guest_tools_state so a guest that has never
-- been probed still appears — "we have never looked at this one" is exactly
-- what an operator needs to see. Windows detection matches the frontend's rule
-- (os-classify.ts): config_ostype prefixed win/w2k plus the two odd ones, or an
-- agent that self-reports mswindows.
-- name: ListGuestToolsFleet :many
SELECT
    v.vmid,
    v.name,
    v.status,
    v.template,
    v.uptime,
    n.name AS node_name,
    s.installed_version,
    s.reboot_required,
    s.agent_version,
    s.agent_running,
    s.detected_at,
    s.stage,
    s.staged_version,
    s.staged_at,
    s.last_error,
    s.last_result_at,
    p.excluded,
    p.target_version AS policy_target_version,
    p.note
FROM vms v
JOIN nodes n ON n.id = v.node_id
LEFT JOIN guest_tools_state s ON s.cluster_id = v.cluster_id AND s.vmid = v.vmid
LEFT JOIN guest_tools_policies p ON p.cluster_id = v.cluster_id AND p.vmid = v.vmid
WHERE v.cluster_id = $1
  AND v.type = 'qemu'
  AND (
      v.ostype = 'mswindows'
      OR v.config_ostype LIKE 'win%'
      OR v.config_ostype LIKE 'w2k%'
      OR v.config_ostype IN ('wxp', 'wvista')
  )
ORDER BY v.vmid;

-- name: UpsertGuestToolsDetection :exec
INSERT INTO guest_tools_state (cluster_id, vmid, installed_version, agent_version, agent_running, detected_at, last_uptime)
VALUES ($1, $2, $3, $4, $5, now(), $6)
ON CONFLICT (cluster_id, vmid) DO UPDATE SET
    installed_version = EXCLUDED.installed_version,
    agent_version     = EXCLUDED.agent_version,
    agent_running     = EXCLUDED.agent_running,
    detected_at       = now(),
    last_uptime       = EXCLUDED.last_uptime;

-- SetGuestToolsStage moves the staging state machine and records what the
-- guest's CD-ROM looked like before we borrowed it.
-- name: SetGuestToolsStage :exec
INSERT INTO guest_tools_state (cluster_id, vmid, stage, staged_version, staged_at, prior_cdrom_key, prior_cdrom_value, last_error)
VALUES ($1, $2, $3, $4, now(), $5, $6, '')
ON CONFLICT (cluster_id, vmid) DO UPDATE SET
    stage             = EXCLUDED.stage,
    staged_version    = EXCLUDED.staged_version,
    staged_at         = now(),
    prior_cdrom_key   = EXCLUDED.prior_cdrom_key,
    prior_cdrom_value = EXCLUDED.prior_cdrom_value,
    last_error        = '';

-- FinishGuestToolsUpdate records a terminal outcome and clears the borrowed
-- CD-ROM bookkeeping, which the reconciler has restored by this point.
-- name: FinishGuestToolsUpdate :exec
UPDATE guest_tools_state
SET stage = $3,
    last_error = $4,
    reboot_required = $5,
    -- Cancelling returns the row to 'idle', where naming a staged version is
    -- just stale: it reads as though something is still queued. Terminal
    -- outcomes keep it, because "which version failed" is worth knowing.
    staged_version = CASE WHEN $3 = 'idle' THEN '' ELSE guest_tools_state.staged_version END,
    last_result_at = now(),
    -- Only forget which drive to put back once it HAS been put back. Clearing
    -- these unconditionally meant a transient restore failure stranded the
    -- virtio-win ISO on the guest forever: the row goes terminal, the in-flight
    -- sweep never looks at it again, and the record of what to restore is gone.
    prior_cdrom_key   = CASE WHEN sqlc.arg('cdrom_restored')::boolean THEN '' ELSE guest_tools_state.prior_cdrom_key END,
    prior_cdrom_value = CASE WHEN sqlc.arg('cdrom_restored')::boolean THEN '' ELSE guest_tools_state.prior_cdrom_value END
WHERE cluster_id = $1 AND vmid = $2;

-- ListGuestToolsPendingCDROMRestore finds guests whose update has finished but
-- whose borrowed drive was never put back, because the restore failed at the
-- time. Retried on later passes so a transient Proxmox error does not strand
-- the ISO on the guest permanently.
-- name: ListGuestToolsPendingCDROMRestore :many
SELECT * FROM guest_tools_state
WHERE stage NOT IN ('staging', 'staged', 'running')
  AND prior_cdrom_key <> ''
ORDER BY last_result_at;

-- name: ClearGuestToolsCDROMRestore :exec
UPDATE guest_tools_state
SET prior_cdrom_key = '', prior_cdrom_value = ''
WHERE cluster_id = $1 AND vmid = $2;

-- name: SetGuestToolsUptime :exec
UPDATE guest_tools_state SET last_uptime = $3 WHERE cluster_id = $1 AND vmid = $2;

-- name: ListGuestToolsInFlight :many
SELECT * FROM guest_tools_state
WHERE stage IN ('staging', 'staged', 'running')
ORDER BY staged_at;

-- name: CountGuestToolsInFlightForCluster :one
SELECT COUNT(*) FROM guest_tools_state
WHERE cluster_id = $1 AND stage IN ('staging', 'staged', 'running');

-- DeleteGuestToolsStateForVanishedGuests drops rows for guests that no longer
-- exist in the cluster.
--
-- @vmids must be a non-nil, possibly-empty slice: pgx encodes a nil slice as
-- SQL NULL, and `NOT (x = ANY(NULL))` is NULL rather than true, so a nil list
-- silently deletes nothing instead of everything.
-- name: DeleteGuestToolsStateForVanishedGuests :exec
DELETE FROM guest_tools_state
WHERE cluster_id = $1 AND NOT (vmid = ANY(@vmids::int[]));
