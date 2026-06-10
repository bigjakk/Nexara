-- Reverts the vm_vmid natural key back to a vms.id surrogate FK (with its
-- ON DELETE CASCADE churn semantics). Rules whose (cluster_id, vmid) no
-- longer resolves to a live vms row get vm_id NULL — the closest equivalent
-- of the old schema, where such rules would already have been cascaded away.

ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS vm_id UUID;

UPDATE alert_rules r
SET vm_id = v.id
FROM vms v
WHERE v.cluster_id = r.cluster_id
  AND v.vmid = r.vm_vmid;

ALTER TABLE alert_rules
    ADD CONSTRAINT alert_rules_vm_id_fkey
    FOREIGN KEY (vm_id) REFERENCES vms(id) ON DELETE CASCADE;

DROP INDEX IF EXISTS idx_alert_rules_vm;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS vm_vmid;
