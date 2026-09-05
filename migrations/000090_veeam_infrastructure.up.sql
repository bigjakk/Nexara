-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000090_veeam_infrastructure.up.sql
-- Phase 3 of the Veeam integration: Veeam's OWN guests on the cluster.
--
-- A Veeam deployment puts machines on the cluster it protects — worker
-- appliances it powers on for a job and off again afterwards, and often the
-- VBR server itself. None of them are backup targets, and a coverage report
-- that lists them as unprotected VMs is a report operators learn to ignore.
-- Measured on the lab: 19 naive alarms, of which 4 are these.
--
-- Purely additive — one new table.

CREATE TABLE IF NOT EXISTS veeam_infrastructure (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    veeam_server_id UUID NOT NULL REFERENCES veeam_servers(id) ON DELETE CASCADE,
    veeam_ref       UUID NOT NULL,
    role            TEXT NOT NULL CHECK (role IN ('worker', 'backup_server')),
    name            TEXT NOT NULL,
    host_name       TEXT NOT NULL DEFAULT '',
    is_disabled     BOOLEAN NOT NULL DEFAULT false,
    is_online       BOOLEAN NOT NULL DEFAULT false,
    cluster_id      UUID REFERENCES clusters(id) ON DELETE SET NULL,
    vmid            INT,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (veeam_server_id, veeam_ref)
);

COMMENT ON TABLE veeam_infrastructure IS 'Guests that belong to the Veeam deployment rather than to the workload it protects: worker appliances (EProxyType "PVE") and the VBR server itself. Sourced from the API, never from a name prefix — "Veeam" appears in the lab worker names and would also match an unrelated guest';
COMMENT ON COLUMN veeam_infrastructure.veeam_ref IS 'The proxy id for a worker, or the managed-server id for the backup server. Veeam''s own stable identity for the row';
COMMENT ON COLUMN veeam_infrastructure.name IS 'The name Veeam knows the machine by, which is the ONLY key available for matching it to a guest: the proxy model carries no smbios uuid and no vmid. For the backup server this is the managedServers FQDN — serverInfo.name is the short form and matches no guest';
COMMENT ON COLUMN veeam_infrastructure.host_name IS 'The Proxmox node a worker was deployed to ("pve-01.example.com"), or the literal "This server" for a proxy role the VBR server fills itself. Informational: Veeam''s node naming does not reliably match Nexara''s, so it is not used to resolve the guest';
COMMENT ON COLUMN veeam_infrastructure.is_online IS 'Workers are normally OFFLINE between runs — Veeam powers them on for a job and off again — so false is the healthy steady state here, not a fault to surface';
COMMENT ON COLUMN veeam_infrastructure.vmid IS 'Paired with cluster_id as the Proxmox-stable guest identity, resolved by an exact case-insensitive name match that must be UNIQUE across the clusters this Veeam server is mapped to. NULL when the machine is not a guest on any mapped cluster at all, which is the normal case for a VBR server running outside the cluster';

CREATE INDEX IF NOT EXISTS idx_veeam_infrastructure_server ON veeam_infrastructure (veeam_server_id);
CREATE INDEX IF NOT EXISTS idx_veeam_infrastructure_guest
    ON veeam_infrastructure (cluster_id, vmid)
    WHERE cluster_id IS NOT NULL AND vmid IS NOT NULL;

CREATE TRIGGER trg_veeam_infrastructure_updated_at
    BEFORE UPDATE ON veeam_infrastructure
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
