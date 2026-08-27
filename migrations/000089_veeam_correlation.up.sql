-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000089_veeam_correlation.up.sql
-- Phase 3 of the Veeam integration: the columns and table that turn a Veeam
-- backup object into "this guest, on this cluster".
--
-- Purely additive — three nullable columns on veeam_backup_objects, one new
-- table, and one CHECK expansion. Nothing existing is rewritten, and every
-- new column carries a DEFAULT or is nullable, so existing rows upgrade in
-- place with no backfill an operator has to run.

-- ---------------------------------------------------------------------------
-- guest_smbios — the Proxmox side of the correlation key.
-- ---------------------------------------------------------------------------
--
-- Veeam's ProxmoxBackupObjectModel.objectId IS the guest's smbios1 uuid, which
-- makes correlation deterministic rather than a name match. That uuid is not
-- in /cluster/resources — it only appears in
-- GET /nodes/{node}/qemu/{vmid}/config — so the collector has to fetch it per
-- guest, and this table is where it is cached so that fetch happens once per
-- guest lifetime rather than once per pass.
--
-- Keyed on the Proxmox-stable (cluster_id, vmid) identity, deliberately NOT on
-- vms.id: the collector deletes and re-inserts guest rows with fresh UUIDs on
-- churn (migrations 000068/000069), so a cache keyed on vms.id would be
-- emptied by exactly the event it exists to survive — and would then re-fetch
-- every guest's config on the next pass. guest_snapshots (000080) is the
-- precedent for the shape; cleanup rides ON DELETE CASCADE from clusters.
--
-- QEMU only. LXC containers have no SMBIOS table at all, so a missing row for
-- a container is the correct steady state, not a gap to retry.
CREATE TABLE IF NOT EXISTS guest_smbios (
    cluster_id   UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    vmid         INT NOT NULL,
    smbios_uuid  TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, vmid)
);

COMMENT ON TABLE guest_smbios IS 'Cache of each QEMU guest''s smbios1 uuid, which is the deterministic join key between Nexara''s inventory and a Veeam backup object. Populated only for clusters with a linked Veeam platform — no Veeam, no per-guest config fetch';
COMMENT ON COLUMN guest_smbios.smbios_uuid IS 'Stored lowercase. Proxmox and Veeam both emit lowercase today, but the correlation join lowers both sides anyway: a case difference here would silently degrade every match to the name-based fallback tier, which is the exact failure this column exists to avoid. An EMPTY string is meaningful, not missing data — it records that the collector read the guest''s config and found no smbios1 uuid. That is what lets correlation distinguish "this guest has no uuid" (where a flagged name match is the best honest answer) from "this guest has not been scanned yet" (where any match would be a guess), and it is also what stops a uuid-less guest being re-read on every single pass forever';

-- Correlation looks the guest up BY uuid within a cluster, so the index leads
-- with the uuid. The primary key already covers the (cluster_id, vmid) reads.
CREATE INDEX IF NOT EXISTS idx_guest_smbios_uuid ON guest_smbios (smbios_uuid);

-- ---------------------------------------------------------------------------
-- veeam_backup_objects — the correlation result.
-- ---------------------------------------------------------------------------
--
-- 000088 deliberately left these out so Phase 3 could pick their shape after
-- measuring against real data. Measured: all 18 lab backup objects carry an
-- objectId, and three of them match no live guest — a deleted VM, a template
-- whose name was reused under a new uuid, and a rebuilt host. Those three are
-- the reason match_method exists and the reason 'name' is a distinct tier
-- rather than a silent fallback: a name match would have reported the rebuilt
-- host as protected by a backup of the machine it replaced.
ALTER TABLE veeam_backup_objects
    ADD COLUMN IF NOT EXISTS cluster_id UUID REFERENCES clusters(id) ON DELETE SET NULL;

ALTER TABLE veeam_backup_objects
    ADD COLUMN IF NOT EXISTS vmid INT;

ALTER TABLE veeam_backup_objects
    ADD COLUMN IF NOT EXISTS match_method TEXT NOT NULL DEFAULT 'none';

-- Named explicitly and added separately from the column so the ADD COLUMN
-- above stays an IF NOT EXISTS no-op on a re-run.
ALTER TABLE veeam_backup_objects
    DROP CONSTRAINT IF EXISTS veeam_backup_objects_match_method_check;

ALTER TABLE veeam_backup_objects
    ADD CONSTRAINT veeam_backup_objects_match_method_check
    CHECK (match_method IN ('none', 'smbios', 'name', 'manual'));

COMMENT ON COLUMN veeam_backup_objects.cluster_id IS 'The Nexara cluster this backup object''s guest lives on, resolved by the correlation pass. NULL means unattributable — either the Veeam platform is not yet mapped to a cluster, or the object is orphaned (its guest no longer exists in the form that was backed up). ON DELETE SET NULL, not CASCADE: removing a cluster must not delete the record that its guests were backed up';
COMMENT ON COLUMN veeam_backup_objects.vmid IS 'Paired with cluster_id as the Proxmox-stable guest identity. Never vms.id — the collector mints a fresh UUID for a guest row on churn, so a foreign key to it would drop correlation on every re-inventory';
COMMENT ON COLUMN veeam_backup_objects.match_method IS 'Which tier resolved this row: smbios (deterministic, the objectId matched a guest''s smbios1 uuid), name (fallback, low confidence — surfaced as such in the UI; only ever applied to a guest the collector has affirmatively recorded as having NO smbios1 uuid, never to one it has not scanned), manual (an operator said so; automatic passes never overwrite it), none (unresolved: the platform is unmapped, the guest has not been scanned yet, or the object is orphaned)';

-- Partial: the orphaned-objects view and the coverage join both ask "which
-- rows resolved", and the unresolved ones are answered by the absence.
CREATE INDEX IF NOT EXISTS idx_veeam_backup_objects_guest
    ON veeam_backup_objects (cluster_id, vmid)
    WHERE cluster_id IS NOT NULL AND vmid IS NOT NULL;

-- ---------------------------------------------------------------------------
-- report_schedules.report_type — allow the multi-provider coverage report.
-- ---------------------------------------------------------------------------
--
-- Same drop-and-readd shape as 000081. The existing backup_compliance report
-- is PBS-only and is left exactly as it is: rewriting it would silently change
-- the output of every schedule an operator already has, so the multi-provider
-- view ships as its own type.
-- ---------------------------------------------------------------------------
-- One-time auto-mapping backfill.
-- ---------------------------------------------------------------------------
--
-- New platforms map themselves on discovery when the install has exactly one
-- active cluster (see UpsertVeeamPlatform). Platforms discovered by an
-- EARLIER release were inserted before that existed, so they sit unmapped and
-- would never get the benefit — this catches them up, once.
--
-- Deliberately a migration and not a recurring sweep: unmapping a platform
-- sets cluster_id back to NULL, so a sweep would re-map it on the next sync
-- tick and silently undo an operator revoking cluster-scoped access. Running
-- exactly once, on the upgrade that introduces correlation, has no such
-- effect. The count guard lives inside the subquery so a multi-cluster
-- install yields NULL rather than a "more than one row" error, and the
-- WHERE clause then updates nothing.
UPDATE veeam_platforms
SET cluster_id = (SELECT c.id FROM clusters c
                   WHERE c.is_active
                     AND (SELECT count(*) FROM clusters WHERE is_active) = 1)
WHERE cluster_id IS NULL
  AND (SELECT count(*) FROM clusters WHERE is_active) = 1;

ALTER TABLE report_schedules
  DROP CONSTRAINT IF EXISTS report_schedules_report_type_check;

ALTER TABLE report_schedules
  ADD CONSTRAINT report_schedules_report_type_check
  CHECK (report_type IN ('resource_utilization', 'capacity_forecast', 'backup_compliance', 'patch_status', 'uptime_summary', 'vm_resource_usage', 'snapshot_inventory', 'veeam_backup_compliance'));
