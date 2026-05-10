import { apiClient } from "@/lib/api-client";

interface ConfigMap {
  [key: string]: unknown;
}

const VM_DISK_KEY_RE = /^(scsi|ide|sata|virtio)\d+$/;
const CT_VOLUME_KEY_RE = /^(rootfs|mp\d+)$/;

/**
 * Resolve a Proxmox volid (e.g. "local-lvm:vm-100-disk-0") to the VM config
 * key (e.g. "scsi0") that owns it. Proxmox's move-disk API takes the config
 * key, not the volid, so callers that only have the storage volid (from
 * /storage/:id/content) must round-trip through the VM config to issue a
 * move.
 *
 * Returns null when the VM config doesn't reference this volid — typically
 * means the disk has been detached or the VM is gone.
 */
export async function resolveVolidToDiskKey(
  clusterId: string,
  vmUuid: string,
  volid: string,
): Promise<string | null> {
  try {
    const config = await apiClient.get<ConfigMap>(
      `/api/v1/clusters/${clusterId}/vms/${vmUuid}/config`,
    );
    for (const [key, val] of Object.entries(config)) {
      if (VM_DISK_KEY_RE.test(key) && typeof val === "string" && val.includes(volid)) {
        return key;
      }
    }
  } catch {
    // VM might not be accessible
  }
  return null;
}

/**
 * Resolve a Proxmox volid (e.g. "local-lvm:subvol-100-disk-0") to the CT
 * config key (e.g. "rootfs", "mp0") that owns it. Mirror of
 * resolveVolidToDiskKey for LXC containers — the move-volume API similarly
 * takes the config key.
 */
export async function resolveVolidToCTVolumeKey(
  clusterId: string,
  ctUuid: string,
  volid: string,
): Promise<string | null> {
  try {
    const config = await apiClient.get<ConfigMap>(
      `/api/v1/clusters/${clusterId}/containers/${ctUuid}/config`,
    );
    for (const [key, val] of Object.entries(config)) {
      if (CT_VOLUME_KEY_RE.test(key) && typeof val === "string" && val.includes(volid)) {
        return key;
      }
    }
  } catch {
    // CT might not be accessible
  }
  return null;
}
