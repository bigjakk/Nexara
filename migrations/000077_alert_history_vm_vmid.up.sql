-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Adds a nullable vm_vmid column to alert_history carrying the stable Proxmox
-- guest identity, mirroring the 000068/000069 rekeys of vm_folder_memberships
-- and alert_rules. alert_history.vm_id (vms.id, ON DELETE SET NULL) nulls out
-- whenever the collector churns a guest's row, which made per-VM alert
-- filtering (folder detail view) silently drop those alerts.
--
-- Backfill resolves vm_vmid through the current vms row where vm_id still
-- points at one. Rows whose vm_id already churned to NULL are unrecoverable
-- and stay NULL — historical only; new rows get vm_vmid from the alert rule
-- (alert_rules.vm_vmid) at insert.
--
-- Idempotent and safe to re-run if interrupted: IF NOT EXISTS guard, and the
-- backfill only touches rows still NULL.

ALTER TABLE alert_history ADD COLUMN IF NOT EXISTS vm_vmid INTEGER;

UPDATE alert_history ah
SET vm_vmid = v.vmid
FROM vms v
WHERE ah.vm_vmid IS NULL
  AND ah.vm_id = v.id;
