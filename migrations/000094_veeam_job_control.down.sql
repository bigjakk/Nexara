-- Reverts 000094: removes the execute:veeam permission family along with every
-- grant of it, and drops the nexara_stopped flag.
--
-- Dropping nexara_stopped loses the record of which runs an operator stopped
-- from Nexara. That is only a loss for veeam_job_failed, which is removed by
-- the same rollback — the paired application release is what evaluates the
-- metric, and a release that does not implement job control cannot have set
-- the column in the first place.
--
-- nexara_initiated's comment is restored to 000088's wording so the schema
-- matches the release being rolled back to.

ALTER TABLE veeam_sessions
  DROP COLUMN IF EXISTS nexara_stopped;

-- Dropping sessions_synced_at returns the watermark to its emptiness test,
-- which is correct for a release without job control: nothing but the poll
-- writes session rows there.
ALTER TABLE veeam_servers
  DROP COLUMN IF EXISTS sessions_synced_at;

COMMENT ON COLUMN veeam_sessions.nexara_initiated IS 'Set when Nexara itself started or stopped the job, taken from the 201 response that carries the session inline. The only way to tell an operator-requested stop from a genuine failure; unused until job control ships';

DELETE FROM role_permissions rp
USING permissions p
WHERE rp.permission_id = p.id AND p.resource = 'veeam' AND p.action = 'execute';

DELETE FROM permissions WHERE resource = 'veeam' AND action = 'execute';
