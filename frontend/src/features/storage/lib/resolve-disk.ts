import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";

interface ConfigMap {
  [key: string]: unknown;
}

/**
 * The config key a volume's move names, or why it cannot move now. Proxmox's
 * move-disk and move-volume APIs take the key, not the volid, so a caller that
 * has only the volid (from /storage/:id/content) reads the guest's config.
 */
export type VolumeResolution =
  | { key: string; reason?: undefined }
  | { key: null; reason: string; slot?: string };

// move_vm_disk's `disk` enum, valid_drive_names_with_unused (qemu-server
// src/PVE/QemuServer/Drive.pm): every bus's disks, the EFI disk, the TPM
// state and the unused volumes.
const VM_MOVABLE_KEY_RE =
  /^((ide|sata|scsi|virtio)\d+|efidisk0|tpmstate0|unused\d+)$/;
// move_volume's `volume` enum, valid_volume_keys_with_unused (pve-container
// src/PVE/LXC/Config.pm).
const CT_VOLUME_KEY_RE = /^(rootfs|mp\d+|unused\d+)$/;

/** The volume a drive or mount point value names: its first, keyless part. */
function volumeOf(value: string): string {
  const first = value.split(",")[0] ?? "";
  return first.replace(/^(file|volume)=/, "");
}

function keyOf(config: ConfigMap, re: RegExp, volid: string): string | null {
  for (const [key, val] of Object.entries(config)) {
    if (re.test(key) && typeof val === "string" && volumeOf(val) === volid) {
      return key;
    }
  }
  return null;
}

/**
 * A VM volume's key. clone_disk (qemu-server src/PVE/QemuServer.pm) mirrors a
 * running VM's attached disks and refuses "cannot move TPM state while VM is
 * running"; an unused volume is copied offline, so it moves either way.
 */
export function vmVolumeKey(
  config: ConfigMap,
  volid: string,
  running: boolean,
): VolumeResolution {
  const key = keyOf(config, VM_MOVABLE_KEY_RE, volid);
  if (!key) return { key: null, reason: "Not in the VM's config (detached?)" };
  if (key === "tpmstate0" && running) {
    return {
      key: null,
      slot: key,
      reason: "TPM state cannot move while the VM runs: shut it down first",
    };
  }
  return { key };
}

/**
 * A container volume's key. move_volume (pve-container src/PVE/API2/LXC.pm)
 * refuses "cannot move volumes of a running container", and an unusedN with
 * "only configured volumes can be moved to another storage".
 */
export function ctVolumeKey(
  config: ConfigMap,
  volid: string,
  running: boolean,
): VolumeResolution {
  const key = keyOf(config, CT_VOLUME_KEY_RE, volid);
  if (!key) {
    return { key: null, reason: "Not in the container's config (detached?)" };
  }
  if (key.startsWith("unused")) {
    return {
      key: null,
      slot: key,
      reason: "An unused volume cannot move to another storage",
    };
  }
  if (running) {
    return {
      key: null,
      slot: key,
      reason: "A running container's volumes cannot move: shut it down first",
    };
  }
  return { key };
}

/** Reads the guest's config and resolves volid in it. */
export async function resolveVolume(
  clusterId: string,
  guest: { id: string; kind: "vm" | "ct"; running: boolean },
  volid: string,
): Promise<VolumeResolution> {
  let config: ConfigMap;
  try {
    config = await apiClient.get<ConfigMap>(
      guest.kind === "ct"
        ? apiPath`/api/v1/clusters/${clusterId}/containers/${guest.id}/config`
        : apiPath`/api/v1/clusters/${clusterId}/vms/${guest.id}/config`,
    );
  } catch {
    return { key: null, reason: "Could not read the guest's config" };
  }
  return guest.kind === "ct"
    ? ctVolumeKey(config, volid, guest.running)
    : vmVolumeKey(config, volid, guest.running);
}
