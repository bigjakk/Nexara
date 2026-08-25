-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Adds a nullable vmid column to audit_log carrying the stable Proxmox guest
-- identity, mirroring the 000076 (task_history) and 000077 (alert_history)
-- rekeys. Backfill only ever writes rows it can resolve, so it is safe to
-- re-run if interrupted.
--
-- Why: the audit reads derive resource_vmid/resource_name at query time from
-- `LEFT JOIN vms v ON v.id::text = a.resource_id`. That join misses in three
-- situations, which together left the fields empty on ~73% of entries on a
-- live install:
--
--   1. vms.id churn. The collector deletes and re-inserts a guest's row on
--      resync (see 000068/000069/000077 for the same bug in other tables), so
--      the UUID recorded in resource_id stops resolving — silently, and at an
--      arbitrary time after the action was audited. This is why the same DRS
--      action showed a VMID one day and a blank the next.
--   2. Deleted guests. A `destroy` entry can never resolve, precisely when the
--      record matters most.
--   3. Handlers that record the VMID rather than the UUID in resource_id
--      (VM create passes strconv.Itoa(req.VMID)), which the UUID join cannot
--      match at all.
--
-- A denormalized column fixes all three: it is written at insert from the
-- UPID (queries/audit_log.sql, so app code cannot forget it) and never
-- re-resolved afterwards.
--
-- Resolution order below is most-authoritative first. `details` retains
-- everything it already carried; nothing is deleted or rewritten.

-- is_guest_upid decides whether a UPID's worker id (field 7) is a guest VMID.
--
-- It is NOT enough that the field is numeric. The worker id is a plain integer
-- for non-guest task types too — cephdestroyosd's is the OSD number, and
-- internal/api/handlers/ceph_osd.go dispatches exactly that through TrackTask.
-- Ungated, "destroy OSD 113" would be recorded as an action on VM 113: it would
-- answer a ?vmids=113 query and be forwarded to a SIEM carrying that guest's
-- identity. An audit row naming the wrong resource is worse than one naming
-- none, so this fails closed — an unrecognised task type yields NULL.
--
-- The allow-list is derived from the task types actually observed carrying a
-- VMID: qm* (QEMU), vz* (LXC, including vzdump), ha* (HA manager), plus the two
-- outliers that carry one without a guest prefix.
--
-- Shared by both insert queries (queries/audit_log.sql) and the backfill below
-- so the three cannot drift apart.
CREATE OR REPLACE FUNCTION is_guest_upid(upid text) RETURNS boolean
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
AS $$
    -- {1,9} digits: Proxmox VMIDs top out at 999999999, and the bound keeps a
    -- pathological all-numeric id from overflowing the ::int cast.
    SELECT split_part($1, ':', 7) ~ '^[0-9]{1,9}$'
       AND (split_part($1, ':', 6) ~ '^(qm|vz|ha)'
            OR split_part($1, ':', 6) IN ('resize', 'move_volume'))
$$;

ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS vmid INTEGER;

-- The WHERE list mirrors the CASE arms so the pass only rewrites rows that
-- actually resolve, instead of touching every row in the table to write NULL
-- over NULL.
UPDATE audit_log a
SET vmid = CASE
        WHEN a.details->>'vmid' ~ '^[0-9]{1,9}$'
            THEN (a.details->>'vmid')::int
        WHEN is_guest_upid(a.details->>'upid')
            THEN split_part(a.details->>'upid', ':', 7)::int
        WHEN a.resource_type IN ('vm', 'container') AND a.resource_id ~ '^[0-9]{1,9}$'
            THEN a.resource_id::int
        ELSE (SELECT v.vmid FROM vms v WHERE v.id::text = a.resource_id)
    END
WHERE a.vmid IS NULL
  AND (a.details->>'vmid' ~ '^[0-9]{1,9}$'
    OR is_guest_upid(a.details->>'upid')
    OR (a.resource_type IN ('vm', 'container') AND a.resource_id ~ '^[0-9]{1,9}$')
    OR EXISTS (SELECT 1 FROM vms v WHERE v.id::text = a.resource_id));

-- Serves the per-guest audit filter (?vmid=) and the name fallback join in
-- ListAuditLogAdvanced / ListRecentAuditLogEnriched. Partial, because the
-- majority of rows on a settings-heavy install carry no guest at all.
CREATE INDEX IF NOT EXISTS idx_audit_log_cluster_vmid
    ON audit_log (cluster_id, vmid)
    WHERE vmid IS NOT NULL;

-- No index is added on vms for the name-fallback join: vms already carries
-- UNIQUE (cluster_id, vmid) from 000004, whose backing index serves it — and
-- which is also why the join cannot multiply an audit row.
