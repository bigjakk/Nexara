-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000094_veeam_job_control.up.sql
-- Phase 4: the Veeam control plane. Adds the execute:veeam permission family
-- and the one column that makes a stop distinguishable from a failure.
--
-- Purely additive: a new permission seeded ON CONFLICT DO NOTHING, and a
-- BOOLEAN NOT NULL DEFAULT false that PostgreSQL fills for existing rows
-- without a rewrite.

-- ---------------------------------------------------------------------------
-- nexara_stopped — the alert-suppression signal.
-- ---------------------------------------------------------------------------
--
-- Veeam records a session cancelled through its API as result "Failed" with
-- isCanceled false and an EMPTY session log (verified live). Nothing in the
-- payload says the stop was deliberate, so veeam_job_failed would fire every
-- time an operator stopped a job from Nexara. This column is what suppresses
-- that, and it is deliberately NOT nexara_initiated.
--
-- The distinction is load-bearing. nexara_initiated means "Nexara started this
-- run"; a run Nexara started can still fail for a completely real reason — a
-- full repository, an unreachable worker — and suppressing on it would make a
-- backup product silently swallow exactly the failure it exists to report.
-- Only a STOP makes the "Failed" result untrustworthy, so only a stop
-- suppresses.
ALTER TABLE veeam_sessions
  ADD COLUMN IF NOT EXISTS nexara_stopped BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN veeam_sessions.nexara_stopped IS 'Set when Nexara asked this session to stop, via POST /jobs/{id}/stop or /sessions/{id}/stop. Suppresses veeam_job_failed: Veeam reports a cancelled run as result "Failed" with isCanceled false and an empty log, so this flag is the only thing that can tell the two apart. A stop made from the Veeam console remains indistinguishable, which is why the alert copy reads "failed or cancelled"';

-- Narrowed from 000088, which described this column as "started or stopped"
-- and as the suppression signal. Job control splits the two: see the reasoning
-- on nexara_stopped above.
COMMENT ON COLUMN veeam_sessions.nexara_initiated IS 'Set when Nexara STARTED this run, taken from the 201 response that carries the session inline. Provenance only — it is not the veeam_job_failed suppression signal, because a run Nexara started can still fail for a real reason. See nexara_stopped';

-- Sessions are read back by veeam_id when a control call has to flag the row
-- the 201 named, and by job for the alert's "latest run per job" scan. The
-- (veeam_server_id, veeam_id) UNIQUE from 000088 serves the first; the second
-- is served by idx_veeam_sessions_job, also from 000088. No new index.

-- ---------------------------------------------------------------------------
-- sessions_synced_at — what "first sync" actually means.
-- ---------------------------------------------------------------------------
--
-- The session poll's watermark was "the newest session row, or the retention
-- floor if there are none". Job control breaks that reading: starting a job
-- writes a session row immediately, so on a server whose session pass has not
-- yet succeeded — it failed and is in backoff, say, while the inventory pass
-- succeeded and listed the jobs — that single row becomes the watermark. The
-- next poll then asks for sessions newer than a few minutes ago instead of the
-- whole retention window, and because a watermark only ever moves forward the
-- backfill never happens.
--
-- Recording when the SESSION PASS last completed separates "no rows yet" from
-- "no poll yet", which is the distinction the watermark actually needs.
--
-- NULL on every existing install, which is correct: it means the next session
-- poll re-reads the retention window once, then narrows. That costs one wider
-- listing per server on upgrade and nothing after.
ALTER TABLE veeam_servers
  ADD COLUMN IF NOT EXISTS sessions_synced_at TIMESTAMPTZ;

COMMENT ON COLUMN veeam_servers.sessions_synced_at IS 'When the session poll last completed for this server. Distinct from last_sync_at, which the INVENTORY pass stamps. NULL means no session poll has ever succeeded, which is what puts the watermark on the retention floor — an emptiness test cannot say that any more, because job control writes session rows of its own';

-- ---------------------------------------------------------------------------
-- execute:veeam
-- ---------------------------------------------------------------------------
--
-- 000087 deliberately left this out: "job control lands in Phase 4 and gets
-- its own migration, so an upgrade that stops at this release cannot grant a
-- capability the code does not yet implement."
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'execute', 'veeam', 'Start, stop, enable and disable Veeam backup jobs')
ON CONFLICT (action, resource) DO NOTHING;

-- Built-in Admin.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
WHERE resource = 'veeam' AND action = 'execute'
ON CONFLICT DO NOTHING;

-- Built-in Operator — "manage all resources except user and role
-- administration", and it already holds manage:veeam and manage:backup.
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions
WHERE resource = 'veeam' AND action = 'execute'
ON CONFLICT DO NOTHING;

-- Built-in Viewer gets nothing here. It holds view:veeam from 000087 and that
-- is the whole of its Veeam access.

-- Custom roles: mirror manage:backup, and ONLY manage:backup.
--
-- This inverts 000087's choice on purpose, and the inversion is the point.
-- There, the analogue was manage:pbs and manage:backup was explicitly refused,
-- because view/manage/delete:veeam are a CREDENTIAL REGISTRY — the right to
-- store a Veeam administrator password — and manage:backup has never conferred
-- that. execute:veeam is the opposite kind of grant: a data-plane right over
-- backup jobs, which is precisely what 000016 defines manage:backup as
-- ("Create, restore, manage backup jobs"). A role whose operator already said
-- "this role runs backup jobs" is the one that should be able to run a Veeam
-- one.
--
-- manage:pbs / manage:veeam are NOT mirrored. Registering a backup server is
-- not the same act as stopping the job protecting production, and a narrow
-- "register PBS servers" role should not silently acquire the second because
-- it holds the first.
--
-- Under-granting is the safe direction here: execute:veeam is a brand-new
-- capability, so a role that misses it loses nothing it had yesterday and an
-- admin can grant it in one click. Over-granting would hand protection-loss
-- powers to roles that never had them.
INSERT INTO role_permissions (role_id, permission_id)
SELECT DISTINCT rp.role_id, vp.id
FROM permissions vp
JOIN permissions bp ON bp.action = 'manage' AND bp.resource = 'backup'
JOIN role_permissions rp ON rp.permission_id = bp.id
WHERE vp.resource = 'veeam' AND vp.action = 'execute'
ON CONFLICT DO NOTHING;
