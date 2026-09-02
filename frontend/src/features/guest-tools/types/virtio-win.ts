/** One virtio-win release published upstream by the Fedora virt group. */
export interface VirtioWinRelease {
  /** Upstream directory version, including the release suffix ("0.1.302-1"). */
  version: string;
  /** Version as it appears in the ISO filename, without the suffix ("0.1.302"). */
  iso_version: string;
  iso_filename: string;
  iso_url: string;
  /** Bytes; 0 when upstream did not answer the size probe. */
  iso_size: number;
  /** True for the one version upstream currently marks stable. */
  is_stable: boolean;
  discovered_at: string;
}

/** A cluster's virtio-win auto-download policy. */
export interface VirtioWinConfig {
  cluster_id: string;
  enabled: boolean;
  storage: string;
  /** Empty means "any online node" — the ISO lands on the storage regardless. */
  node: string;
  /** Empty means "follow upstream stable". */
  target_version: string;
  prune_enabled: boolean;
  last_check_at: string | null;
  last_error: string;
  /** Five-field cron expression; empty means every six hours. */
  check_schedule: string;
  /** IANA zone the cron is read in; empty means the server's own zone. */
  check_timezone: string;
  /** Null means a check is due now — just enabled, or never run. */
  next_check_at: string | null;
  /**
   * What the cluster will actually hold: the pin when set, otherwise upstream
   * stable. Resolved server-side so the UI never re-derives the precedence rule.
   */
  effective_version: string;
  /** The download root in force. Instance-wide; see VirtioWinMirror. */
  source_url: string;
}

export interface VirtioWinConfigRequest {
  enabled: boolean;
  storage: string;
  node: string;
  target_version: string;
  /**
   * Optional on the wire: prune DELETES ISOs, and an omitted key preserves the
   * stored value rather than reading as false. Always send it from a form the
   * operator actually saw.
   */
  prune_enabled?: boolean;
  check_schedule: string;
  check_timezone: string;
}

export type VirtioWinDownloadStatus =
  | "pending"
  | "running"
  | "succeeded"
  | "failed";

export interface VirtioWinDownload {
  id: string;
  cluster_id: string;
  node: string;
  storage: string;
  version: string;
  filename: string;
  status: VirtioWinDownloadStatus;
  upid: string;
  error: string;
  triggered_by: "scheduler" | "manual";
  started_at: string;
  finished_at: string | null;
}

/** Body for POST /api/v1/clusters/:cid/virtio-win/download */
export interface VirtioWinDownloadRequest {
  /** Omit to download the cluster's effective target version. */
  version?: string;
}

/**
 * The download endpoint answers with the dispatched row, or one of these
 * markers. Both mean the intent is satisfied, not that anything went wrong:
 * `already_present` when the ISO is on the storage, `already_running` when an
 * identical download is in flight (two operators clicking at once, or a click
 * racing a scheduler tick).
 */
export interface VirtioWinAlreadyPresent {
  status: "already_present";
  version: string;
}

export interface VirtioWinAlreadyRunning {
  status: "already_running";
  version: string;
}

export type VirtioWinDownloadResult =
  | VirtioWinDownload
  | VirtioWinAlreadyPresent
  | VirtioWinAlreadyRunning;

export function isAlreadyPresent(
  result: VirtioWinDownloadResult,
): result is VirtioWinAlreadyPresent {
  // Discriminates on the union directly rather than through a cast: the
  // members' `status` fields have no overlapping values, so this narrows.
  return result.status === "already_present";
}

export function isAlreadyRunning(
  result: VirtioWinDownloadResult,
): result is VirtioWinAlreadyRunning {
  return result.status === "already_running";
}

/**
 * The instance-wide download source. One value for the whole install, not per
 * cluster: the release catalog it fills is global, and being cut off from
 * fedorapeople.org is a property of the install.
 */
export interface VirtioWinMirror {
  /** The configured override; empty means follow upstream. */
  base_url: string;
  /** The root actually in use — the override, or upstream. */
  effective_url: string;
  /** What "no override" resolves to, so the UI need not hardcode a copy. */
  upstream_url: string;
}

/**
 * The two 422 codes the source endpoint answers with. Neither is a failure:
 * each is a prompt the card renders inline with its own confirmation, which is
 * why they are kept out of the global mutation error toast.
 */
export const MIRROR_CONFIRM_CODES = [
  "insecure_source_confirm_required",
  "private_address_confirm_required",
] as const;

export interface VirtioWinMirrorRequest {
  base_url: string;
  /** Confirms a base resolving to a private address, which a mirror always is. */
  allow_private_address?: boolean;
  /** Confirms a plain-http base, which fetches the ISO unauthenticated. */
  allow_insecure?: boolean;
}

/**
 * Result of running a cluster's check off-schedule. The config always comes
 * back with the timestamps the check just wrote; a download is present only
 * when the check found the target ISO missing and dispatched a fetch for it.
 */
export interface VirtioWinCheckResult {
  config: VirtioWinConfig;
  download?: VirtioWinDownload;
}
