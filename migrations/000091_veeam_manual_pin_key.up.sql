-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000091_veeam_manual_pin_key.up.sql
-- Records WHICH guest an operator's manual backup-object mapping was pinned
-- to, so the pin can be invalidated when that guest is replaced.
--
-- Purely additive — one column with a DEFAULT.
--
-- The problem it solves. A manual mapping is deliberately exempt from
-- automatic re-correlation: that is what makes an operator's decision stick.
-- But the pin is stored as (cluster_id, vmid), and Proxmox reuses a VMID once
-- the guest holding it is destroyed. Delete the pinned guest, let Proxmox hand
-- 105 to something new, and the new guest inherits the old one's restore
-- points and reports as protected — the exact "protected by a backup of the
-- machine it replaced" failure that correlating on the SMBIOS uuid exists to
-- prevent, except a manual pin can never self-heal from it and the orphan
-- listing cannot surface it either.
--
-- Recording the guest's SMBIOS uuid as it stood when the pin was made gives
-- the correlation pass something to check the pin against. It invalidates only
-- on a genuine identity change: a guest row churned by the collector keeps its
-- guest_smbios entry, and a rename does not touch the uuid, so neither
-- disturbs the pin.

ALTER TABLE veeam_backup_objects
    ADD COLUMN IF NOT EXISTS manual_guest_key TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN veeam_backup_objects.manual_guest_key IS 'The pinned guest''s smbios1 uuid as it stood when an operator created the manual mapping. Empty means the guest had none to record (or the row is not manually mapped), in which case the pin cannot be identity-checked and is trusted as given. Read ONLY while match_method = ''manual'' — a leftover value on a row that has since been re-resolved automatically is inert';
