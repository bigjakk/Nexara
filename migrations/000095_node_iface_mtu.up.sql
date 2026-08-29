-- Persist the interface MTU that the collector already fetches from Proxmox.
--
-- GET /nodes/{node}/network returns `mtu`, and internal/proxmox decodes it into
-- NetworkInterface.MTU, but node_network_interfaces had no column for it, so the
-- value was read and discarded on every sync. The DB-backed inventory endpoint
-- (/nodes/{node_id}/network-interfaces) therefore omitted MTU entirely, while the
-- live passthrough (/networks/{node_name}) reported it.
--
-- PVE omits `mtu` for interfaces that do not set one explicitly (they inherit the
-- 1500 default), so 0 means "not configured" — it is never a valid MTU, since the
-- Proxmox schema constrains the field to 1280..65520.
ALTER TABLE node_network_interfaces
    ADD COLUMN IF NOT EXISTS mtu INT NOT NULL DEFAULT 0;
