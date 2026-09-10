-- 000102_rolling_update_in_place.up.sql
-- Let a rolling-update job upgrade a node WITHOUT draining it first.
--
-- Additive (new columns with defaults) — safe for in-place upgrade, no operator
-- action required. Both defaults reproduce today's behaviour exactly, so every
-- existing job and every job created by an un-updated client is unaffected.
--
-- WHY ----------------------------------------------------------------------
-- startNode always drains. Drain picks a migration target from the cluster's
-- other online nodes and fails the node outright when there is none:
--
--   "no available target nodes for migration"   (rolling/orchestrator.go)
--
-- So a single-node cluster with any running guest cannot run a rolling update
-- at all — and a single-node cluster is exactly the shape that most wants a
-- plain "apply the pending updates on this node" button. The drain is also
-- unnecessary work on a multi-node cluster when no reboot is planned: apt on a
-- PVE node does not require the guests to be elsewhere.
--
-- drain_guests = false means "upgrade this node in place, leave its guests
-- running". It changes nothing else: the same SSH path, package excludes, step
-- tracking, cancellation and cleanup all still apply.

ALTER TABLE rolling_update_jobs
    ADD COLUMN IF NOT EXISTS drain_guests BOOLEAN NOT NULL DEFAULT true;

-- Records "the upgrade succeeded and a reboot is still pending".
--
-- Both reboot paths already refuse to reboot a node that still has running
-- guests, which is the correct safety property and is kept. For a drained job
-- that refusal is a genuine failure — something landed on the node mid-drain.
-- For an in-place job the guests are running because the operator asked for
-- that, and a pending reboot is the expected outcome rather than an error:
-- the automated path raises its own reboot flag whenever apt leaves
-- /var/run/reboot-required behind, which any kernel update does. Without this
-- column a routine kernel upgrade would apply cleanly and still end the job
-- "failed", which is both alarming and wrong.
ALTER TABLE rolling_update_nodes
    ADD COLUMN IF NOT EXISTS reboot_required BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN rolling_update_jobs.drain_guests IS 'False upgrades each node in place, leaving its guests running (no migration, no target node needed)';
COMMENT ON COLUMN rolling_update_nodes.reboot_required IS 'Upgrade applied but a reboot is still pending; set when the node could not be rebooted because guests are running on it';
