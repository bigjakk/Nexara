-- Adds Proxmox VE version cache to clusters table.
-- Populated by the collector each sync cycle via /version.
-- Used by frontend to feature-gate version-dependent UI
-- (e.g. OCI image pull requires PVE 9.1+).
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS pve_version TEXT NOT NULL DEFAULT '';
