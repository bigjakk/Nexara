import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { isOkExit } from "@/components/layout/task-status";
import type { TaskStatusResponse } from "@/features/vms/types/vm";

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

const TASK_POLL_MS = 2_000;
const TASK_POLL_MAX_MS = 30_000;
/** How long the status may go unread before the wait gives up on it. */
export const TASK_UNREADABLE_MS = 120_000;

/** How a wait for a move's task ended. */
export type TaskOutcome =
  | { status: "ended"; ok: boolean; exit: string }
  /** The status could not be read for TASK_UNREADABLE_MS: it may still run. */
  | { status: "unknown" }
  /** The session ended or the batch was stopped; the task was not cancelled. */
  | { status: "halted" };

/** Resolves after ms, or at once when stop is aborted. */
function pause(ms: number, stop: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(done, ms);
    function done() {
      clearTimeout(timer);
      stop.removeEventListener("abort", done);
      resolve();
    }
    stop.addEventListener("abort", done);
  });
}

/**
 * Waits for a move's task to end. A VM's disk move holds the config lock for
 * its whole run (move_vm_disk's worker runs in lock_config, qemu-server
 * src/PVE/API2/Qemu.pm, and a second move's worker gives up after the lock's
 * 10 s timeout); a container's sets a 'disk' lock that refuses the next. So a
 * batch starts a guest's next move only once this says the task ended. A read
 * that fails is no failed move: it is retried, backing off, and only a status
 * unread for TASK_UNREADABLE_MS is "unknown". Stop ends the wait at once.
 */
export async function waitForTask(
  clusterId: string,
  upid: string,
  ended: () => boolean,
  stop: AbortSignal,
): Promise<TaskOutcome> {
  const stopped = new Promise<null>((resolve) => {
    if (stop.aborted) resolve(null);
    stop.addEventListener("abort", () => {
      resolve(null);
    });
  });
  let failingSince: number | null = null;
  let delay = TASK_POLL_MS;
  for (;;) {
    if (ended() || stop.aborted) return { status: "halted" };
    try {
      const task = await Promise.race([
        apiClient.get<TaskStatusResponse>(
          apiPath`/api/v1/clusters/${clusterId}/tasks/${upid}`,
        ),
        stopped,
      ]);
      if (task === null) return { status: "halted" };
      failingSince = null;
      delay = TASK_POLL_MS;
      if (task.status === "stopped") {
        return {
          status: "ended",
          ok: isOkExit(task.exit_status),
          exit: task.exit_status,
        };
      }
    } catch {
      const now = Date.now();
      failingSince ??= now;
      if (now - failingSince >= TASK_UNREADABLE_MS)
        return { status: "unknown" };
      delay = Math.min(delay * 2, TASK_POLL_MAX_MS);
    }
    await pause(delay, stop);
  }
}

/** What a row says when its move was left running, or never started. */
export const MOVE_NOTES = {
  unknown: "Status unknown: the move may still be running. Check Tasks.",
  detached: "Still running in Proxmox: follow it in Tasks",
  skipped: "Not started",
} as const;
