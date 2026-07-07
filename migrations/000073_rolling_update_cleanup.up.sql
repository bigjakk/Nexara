-- Reconciled cleanup for rolling updates.
--
-- A rolling update temporarily holds cluster state: Nexara DRS disabled,
-- Proxmox native CRS auto-rebalance paused, HA rules disabled, and
-- passthrough guests shut down in place. Restores used to fire exactly once
-- at the moment a job finished, failed, or was cancelled — and the most
-- common failure cause (cluster unreachable) made that one attempt fail too,
-- leaking the held state permanently. These columns let the orchestrator
-- reconcile instead: terminal jobs are flagged cleanup_pending and swept
-- with backoff until every held resource is confirmed released.
--
-- jobs.disabled_ha_rules moves the disabled-HA-rule record from per-node to
-- job scope: rules are cluster-wide objects, and per-node re-enablement
-- could prematurely re-enable a rule another still-draining node depended
-- on (parallelism >= 2). The per-node column is retained read-only so jobs
-- in flight across this upgrade still get their rules restored.
--
-- nodes.stopped_passthrough_json records passthrough guests the drain shut
-- down (they cannot live-migrate) that have not been confirmed running
-- again, so failure/cancel paths and the cleanup sweep can start them back
-- up. NULL means "recorded by a pre-upgrade version" — consumers fall back
-- to deriving the list from guests_json.
--
-- Pre-existing terminal jobs keep cleanup_pending = false (the column
-- default), so this migration never triggers restores for historical jobs
-- whose state may have long since been changed deliberately.

ALTER TABLE rolling_update_jobs
    ADD COLUMN IF NOT EXISTS disabled_ha_rules JSONB,
    ADD COLUMN IF NOT EXISTS cleanup_pending BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS cleanup_attempts INT NOT NULL DEFAULT 0;

ALTER TABLE rolling_update_nodes
    ADD COLUMN IF NOT EXISTS stopped_passthrough_json JSONB;

-- Default applies to rows inserted from now on (SET DEFAULT does not touch
-- existing rows), so NULL keeps meaning exactly "written by a pre-000073
-- version" and the legacy derivation fallback has a real deletion date.
ALTER TABLE rolling_update_nodes
    ALTER COLUMN stopped_passthrough_json SET DEFAULT '[]'::jsonb;

-- The cleanup sweep runs every scheduler tick; keep its scan cheap.
CREATE INDEX IF NOT EXISTS idx_rolling_update_jobs_cleanup
    ON rolling_update_jobs(cleanup_pending) WHERE cleanup_pending = true;
