import { useMemo } from "react";
import { apiPath } from "@/lib/api-path";
import { HistoryTimeline } from "@/features/history/components/HistoryTimeline";
import type { HistoryTarget } from "@/features/history/types/history";
import { guestHistoryKind } from "../lib/guest-history-kind";
import { LiveBlockJobRows } from "./LiveBlockJobRows";

/**
 * A guest's History tab: everything that happened to it, newest first — its
 * tasks in Nexara and in Proxmox with how each ended, its config and HA
 * changes, and the status changes no task explains. Rows expand to the task's
 * Proxmox log.
 *
 * Keyed on the Proxmox VMID within the cluster, as the server is: the history
 * outlives the guest's inventory row, and the other cluster's VM of the same
 * number is a different guest.
 *
 * A running VM's block jobs sit above the first page as live rows — the
 * Overview once showed them in a card of their own, which was empty almost
 * always.
 */
export function GuestHistoryTab({
  clusterId,
  vmId,
  vmid,
  showBlockJobs,
}: {
  clusterId: string;
  /** The guest's inventory row, which the block-jobs read is keyed on. */
  vmId: string;
  vmid: number;
  /** A running QEMU VM: the only guest with block jobs to read. */
  showBlockJobs: boolean;
}) {
  const target = useMemo<HistoryTarget>(
    () => ({
      clusterId,
      path: (query) =>
        apiPath`/api/v1/clusters/${clusterId}/guests/${String(vmid)}/history?${query}`,
    }),
    [clusterId, vmid],
  );
  return (
    <HistoryTimeline
      kind={guestHistoryKind}
      target={target}
      enabled={vmid > 0}
      rowsAbove={
        showBlockJobs
          ? (colSpan) => (
              <LiveBlockJobRows
                clusterId={clusterId}
                vmId={vmId}
                colSpan={colSpan}
              />
            )
          : undefined
      }
    />
  );
}
