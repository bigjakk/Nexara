import { apiPath } from "@/lib/api-path";
import type { HistoryTarget } from "@/features/history/types/history";

/**
 * Where a guest's History is read from. Keyed on the Proxmox VMID within the
 * cluster, as the server is: the history outlives the guest's inventory row.
 */
export function guestHistoryTarget(
  clusterId: string,
  vmid: number,
): HistoryTarget {
  return {
    clusterId,
    path: (query) =>
      apiPath`/api/v1/clusters/${clusterId}/guests/${String(vmid)}/history?${query}`,
  };
}
