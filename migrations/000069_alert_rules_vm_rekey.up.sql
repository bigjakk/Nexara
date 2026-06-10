-- ⚠️ BREAKING UPGRADE — automated data backfill (no manual steps required)
-- This migration re-keys alert_rules' VM scope and backfills existing rows in
-- place. Existing VM-scoped rules are preserved automatically; the procedure
-- and its recovery semantics are documented inline below.
--
-- WHY ----------------------------------------------------------------------
-- alert_rules.vm_id referenced the ephemeral surrogate vms.id with
-- ON DELETE CASCADE. The collector prunes and re-inserts a vms row whenever
-- Proxmox stops listing a guest beyond the grace window; the cascade then
-- silently DELETED the user's alert rule with no audit trail. This is the
-- same bug class as the folder memberships re-keyed in 000068.
--
-- The fix stores the stable Proxmox identity instead: the rule's existing
-- cluster_id plus a new vm_vmid column. The alert engine resolves the live
-- vms row at evaluation time, so rules survive collector churn — and even a
-- destroy+recreate that reuses the VMID inherits the rule, vCenter-style
-- (intentional, matching folder-membership semantics).
--
-- API CHANGE ---------------------------------------------------------------
-- Alert-rule create/update/read payloads now carry "vm_vmid" (integer
-- Proxmox VMID) instead of "vm_id" (vms-row UUID). The bundled frontend is
-- updated in the same release; external API consumers creating VM-scoped
-- rules must switch fields.
--
-- RECOVERY -----------------------------------------------------------------
-- The whole migration runs in one transaction (golang-migrate default). If
-- it fails or is interrupted it rolls back to the surrogate-keyed schema
-- with no data loss; re-running it re-attempts the backfill cleanly.

-- 1. Add the natural-key column (nullable: only vm-scoped rules use it).
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS vm_vmid INT;

-- 2. Backfill from the live vms rows the surrogate currently points at, and
--    repair any vm-scoped rule missing its cluster_id while we're here.
UPDATE alert_rules r
SET vm_vmid    = v.vmid,
    cluster_id = COALESCE(r.cluster_id, v.cluster_id)
FROM vms v
WHERE v.id = r.vm_id;

-- 3. Drop the surrogate column — and with it the ON DELETE CASCADE to vms.
--    A vm-scoped rule whose surrogate no longer resolved keeps vm_vmid NULL;
--    the engine skips those at evaluation instead of the schema deleting them.
ALTER TABLE alert_rules DROP COLUMN IF EXISTS vm_id;

-- 4. Evaluation-time lookup support (engine resolves rules by natural key).
CREATE INDEX IF NOT EXISTS idx_alert_rules_vm
    ON alert_rules(cluster_id, vm_vmid) WHERE vm_vmid IS NOT NULL;
