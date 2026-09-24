import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type { GuestSnapshotRow } from "../types/snapshots";

/** Central inventory, collected by the backend snapshot sync loop.
 * snapshot_change WS events invalidate this key (useEventInvalidation). */
export function useGuestSnapshots() {
  return useQuery({
    queryKey: ["guest-snapshots"],
    queryFn: () =>
      apiClient.page<GuestSnapshotRow>(apiPath`/api/v1/guest-snapshots`),
  });
}

interface ResyncGuestSnapshotsParams {
  clusterId: string;
  vmid: number;
}

/** One-guest cache refresh straight from Proxmox — used by the per-row
 * Refresh action and after a delete task completes, so the row converges
 * without waiting for the next collector pass. */
export function useResyncGuestSnapshots() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, vmid }: ResyncGuestSnapshotsParams) =>
      apiClient.post<{ count: number }>(
        apiPath`/api/v1/clusters/${clusterId}/guest-snapshots/resync`,
        { vmid },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["guest-snapshots"] });
    },
  });
}
