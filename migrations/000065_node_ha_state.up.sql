-- Track per-node HA state (e.g. "online", "maintenance", "fence") as reported by
-- the Proxmox HA manager, so the UI can show when a node is in maintenance mode.
-- Populated by the collector each sync from /cluster/ha/status/manager_status.
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS ha_state TEXT NOT NULL DEFAULT '';
