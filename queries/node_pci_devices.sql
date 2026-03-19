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
    last_seen_at = now()
RETURNING *;

-- name: ListNodePCIDevicesByNode :many
SELECT * FROM node_pci_devices WHERE node_id = $1 ORDER BY pci_id;

-- name: DeleteStaleNodePCIDevices :exec
DELETE FROM node_pci_devices WHERE node_id = $1 AND last_seen_at < $2;
