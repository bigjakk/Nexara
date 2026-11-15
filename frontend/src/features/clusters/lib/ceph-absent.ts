import { ApiClientError } from "@/lib/api-client";

/**
 * Proxmox's own words for "this cluster has no Ceph": GET /nodes/{node}/ceph/status
 * runs pve-manager's PVE::Ceph::Tools::check_ceph_inited first, which dies with
 * "binary not installed: /usr/bin/ceph-mon" (check_ceph_installed) or
 * "pveceph configuration not initialized - missing ..." and nothing else.
 * Nexara relays the sentence verbatim (handlers.mapProxmoxError).
 */
const NO_CEPH = [
  /^binary not installed: /,
  /^pveceph configuration not initialized/,
];

/**
 * Whether a Ceph status read failed because the cluster said it has no Ceph.
 * Anything else (still loading, unreachable, refused) is not that answer.
 */
export function reportsNoCeph(error: unknown): boolean {
  if (!(error instanceof ApiClientError)) return false;
  const message = error.body.message.trim();
  return NO_CEPH.some((re) => re.test(message));
}
