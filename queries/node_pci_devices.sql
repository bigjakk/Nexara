-- name: UpsertNodePCIDevice :one
INSERT INTO node_pci_devices (node_id, cluster_id, pci_id, class, device_name, vendor_name, device, vendor,
                               iommu_group, subsystem_device, subsystem_vendor, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
ON CONFLICT (node_id, pci_id) DO UPDATE SET
    class = EXCLUDED.class,
    device_name = EXCLUDED.device_name,
    vendor_name = EXCLUDED.vendor_name,
    device = EXCLUDED.device,
    vendor = EXCLUDED.vendor,
    iommu_group = EXCLUDED.iommu_group,
    subsystem_device = EXCLUDED.subsystem_device,
    subsystem_vendor = EXCLUDED.subsystem_vendor,
    -- last_seen_at marks every sync; updated_at moves only when the row's content actually
    -- changed, so it answers "when did this row last change?" rather than "when was it last polled?".
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
RETURNING *;

-- name: ListNodePCIDevicesByNode :many
SELECT * FROM node_pci_devices WHERE node_id = $1 ORDER BY pci_id;

-- name: DeleteStaleNodePCIDevices :exec
-- Grace-windowed, DB-clock prune (mirrors DeleteStaleVMsForNodes in vms.sql).
DELETE FROM node_pci_devices
WHERE node_id = $1
  AND last_seen_at < now() - make_interval(secs => @grace_seconds::int);
