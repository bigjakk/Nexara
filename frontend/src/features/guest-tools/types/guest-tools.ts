/** How aggressively Nexara acts on a cluster's Windows guests. */
export type GuestToolsMode =
  /** Nothing at all. */
  | "disabled"
  /** Detect installed versions only; never writes to any guest. */
  | "report"
  /** Also stage updates for guests that are behind. */
  | "staged";

export interface GuestToolsConfig {
  cluster_id: string;
  mode: GuestToolsMode;
  /** Empty means follow the cluster's virtio-win ISO target. */
  target_version: string;
  snapshot_before: boolean;
  max_concurrent: number;
  /** What a guest with no override resolves to, computed server-side. */
  effective_version: string;
  /** The virtio-win storage this cluster uses. Empty means staging can't work. */
  iso_storage: string;
}

export interface GuestToolsConfigRequest {
  mode: GuestToolsMode;
  target_version: string;
  max_concurrent: number;
  /**
   * Optional on the wire: this is the rollback for a driver swap that can
   * leave a guest unbootable, so an omitted key preserves the stored value.
   */
  snapshot_before?: boolean;
}

/**
 * Where a guest is in the staging state machine.
 * `staged` means the scheduled task is registered and will fire at next boot;
 * `running` means it was also started on demand.
 */
export type GuestToolsStage =
  | "idle"
  | "staging"
  | "staged"
  | "running"
  | "succeeded"
  | "failed";

export interface GuestToolsGuest {
  vmid: number;
  name: string;
  node: string;
  status: string;
  template: boolean;
  /** DisplayVersion reported by the guest, e.g. "0.1.285". Empty = never read. */
  installed_version: string;
  agent_version: string;
  agent_running: boolean;
  detected_at: string | null;
  stage: GuestToolsStage;
  /**
   * The installer returned 3010: installed, but a driver that was in use only
   * swaps at the guest's next restart. Neither an error nor fully done.
   */
  reboot_required: boolean;
  staged_version: string;
  staged_at: string | null;
  last_error: string;
  last_result_at: string | null;
  excluded: boolean;
  policy_target_version: string;
  note: string;
  /** The effective target for THIS guest, after per-guest and cluster pins. */
  target_version: string;
  /**
   * up_to_date and needs_update are both false when the installed version is
   * unknown — "we have never looked" is a distinct state from "current".
   */
  up_to_date: boolean;
  needs_update: boolean;
}

export interface GuestToolsPolicyRequest {
  target_version: string;
  note: string;
  /** Optional: an omitted key preserves the stored exclusion. */
  excluded?: boolean;
}

export interface GuestToolsUpdateRequest {
  /** Start the installer now instead of waiting for the guest's next boot. */
  run_now: boolean;
}

export interface GuestToolsUpdateResponse {
  vmid: number;
  version: string;
  cdrom: string;
  run_now: boolean;
  snapshot: string;
  stage: GuestToolsStage;
}

export interface GuestToolsDetectResponse {
  installed_version: string;
  agent_version: string;
  agent_running: boolean;
  installed: boolean;
}
