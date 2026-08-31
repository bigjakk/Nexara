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
  /**
   * Operator-supplied. Upstream publishes no ISO checksum — its CHECKSUM file
   * covers only the RPMs — so this is empty unless somebody pasted one in.
   */
  checksum: string;
  checksum_algorithm: string;
  published_at: string | null;
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
  /**
   * What the cluster will actually hold: the pin when set, otherwise upstream
   * stable. Resolved server-side so the UI never re-derives the precedence rule.
   */
  effective_version: string;
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
