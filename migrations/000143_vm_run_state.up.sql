-- 000143_vm_run_state.up.sql
-- vms.run_state: QEMU's own run state for a VM whose process is up but whose
-- vCPUs are halted (io-error, suspended, watchdog, guest-panicked, shutdown,
-- internal-error, debug), '' otherwise. It sits beside vms.status, which keeps
-- saying "running" for such a VM, so the counts, filters, DRS, rolling-update
-- drains and alerts that ask "is it running?" are unchanged. Written by the
-- collector from the qmpstatus Proxmox reports.
--
-- Additive, with a default: safe for an in-place upgrade. Existing rows get ''
-- and the collector fills the column on its next pass.

ALTER TABLE vms ADD COLUMN IF NOT EXISTS run_state TEXT NOT NULL DEFAULT '';
