-- name: UpsertNodePCIDevices :exec
-- One statement — and therefore one transaction and at most one WAL flush —
-- for a whole node's PCI inventory, instead of one implicit transaction per
-- device. On a 3-node cluster that is ~265 commits per sync collapsed into 3.
--
-- The DO UPDATE is gated: an unchanged device whose last_seen_at is still
-- inside the heartbeat window writes NOTHING, so a sweep over unchanged
-- hardware produces no dirty tuples, no WAL and no fsync. @heartbeat_seconds
-- MUST stay well below the DeleteStaleNodePCIDevices grace window, or the
-- prune below would delete rows the heartbeat has not refreshed yet.
--
-- DISTINCT ON dedupes the input on the conflict key: Postgres rejects an
-- ON CONFLICT DO UPDATE that would touch the same row twice in one statement,
-- which a per-row loop could never hit but a set-based upsert can.
--
-- The batch arrives as one jsonb array rather than N parallel text[]/int[]
-- parameters because sqlc cannot parse multi-argument unnest(...).
INSERT INTO node_pci_devices (node_id, cluster_id, pci_id, class, device_name, vendor_name, device, vendor,
                               iommu_group, subsystem_device, subsystem_vendor, last_seen_at)
SELECT DISTINCT ON (d.pci_id)
       @node_id::uuid, @cluster_id::uuid, d.pci_id, d.class, d.device_name, d.vendor_name, d.device, d.vendor,
       d.iommu_group, d.subsystem_device, d.subsystem_vendor, now()
FROM jsonb_to_recordset(@devices::jsonb) AS d(
        pci_id text, class text, device_name text, vendor_name text,
        device text, vendor text, iommu_group int,
        subsystem_device text, subsystem_vendor text
     )
ORDER BY d.pci_id
ON CONFLICT (node_id, pci_id) DO UPDATE SET
    class = EXCLUDED.class,
    device_name = EXCLUDED.device_name,
    vendor_name = EXCLUDED.vendor_name,
    device = EXCLUDED.device,
    vendor = EXCLUDED.vendor,
    iommu_group = EXCLUDED.iommu_group,
    subsystem_device = EXCLUDED.subsystem_device,
    subsystem_vendor = EXCLUDED.subsystem_vendor,
    -- last_seen_at marks every sync that gets this far; updated_at moves only when the
    -- row's content actually changed, so it answers "when did this row last change?"
    -- rather than "when was it last polled?". The WHERE below lets a heartbeat-only
    -- refresh through, so this CASE is still what keeps updated_at honest.
    updated_at = CASE WHEN (
        node_pci_devices.class,
        node_pci_devices.device_name,
        node_pci_devices.vendor_name,
        node_pci_devices.device,
        node_pci_devices.vendor,
        node_pci_devices.iommu_group,
        node_pci_devices.subsystem_device,
        node_pci_devices.subsystem_vendor
    ) IS DISTINCT FROM (
        EXCLUDED.class,
        EXCLUDED.device_name,
        EXCLUDED.vendor_name,
        EXCLUDED.device,
        EXCLUDED.vendor,
        EXCLUDED.iommu_group,
        EXCLUDED.subsystem_device,
        EXCLUDED.subsystem_vendor
    ) THEN now() ELSE node_pci_devices.updated_at END,
    last_seen_at = now()
WHERE (
    node_pci_devices.class,
    node_pci_devices.device_name,
    node_pci_devices.vendor_name,
    node_pci_devices.device,
    node_pci_devices.vendor,
    node_pci_devices.iommu_group,
    node_pci_devices.subsystem_device,
    node_pci_devices.subsystem_vendor
) IS DISTINCT FROM (
    EXCLUDED.class,
    EXCLUDED.device_name,
    EXCLUDED.vendor_name,
    EXCLUDED.device,
    EXCLUDED.vendor,
    EXCLUDED.iommu_group,
    EXCLUDED.subsystem_device,
    EXCLUDED.subsystem_vendor
) OR node_pci_devices.last_seen_at < now() - make_interval(secs => @heartbeat_seconds::int);

-- name: ListNodePCIDevicesByNode :many
SELECT * FROM node_pci_devices WHERE node_id = $1 ORDER BY pci_id;

-- name: DeleteStaleNodePCIDevices :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStaleVMsForNodes in vms.sql).
-- The grace window MUST exceed the upsert's @heartbeat_seconds above.
DELETE FROM node_pci_devices
WHERE node_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);
