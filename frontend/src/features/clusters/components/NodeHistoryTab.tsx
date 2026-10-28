import { useMemo } from "react";
import { apiPath } from "@/lib/api-path";
import { HistoryTimeline } from "@/features/history/components/HistoryTimeline";
import type { HistoryTarget } from "@/features/history/types/history";
import { guestLinksByVmid } from "@/features/vms/lib/guest-links";
import { useClusterVMs } from "../api/cluster-queries";
import { nodeHistoryKind } from "../lib/node-history-kind";

/**
 * A node's History tab: what happened to it, newest first — its own actions
 * and its parts', the tasks that ran on it, its status changes and the guests
 * that arrived or left — with a Guest column. Its guests' own activity comes
 * in with Include guest activity.
 */
export function NodeHistoryTab({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const { data: vms } = useClusterVMs(clusterId);
  const guestLinks = useMemo(
    () => guestLinksByVmid(vms, clusterId),
    [vms, clusterId],
  );
  const target = useMemo<HistoryTarget>(
    () => ({
      clusterId,
      path: (query) =>
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/history?${query}`,
    }),
    [clusterId, nodeName],
  );
  return (
    <HistoryTimeline
      kind={nodeHistoryKind}
      target={target}
      guestLink={(vmid) => guestLinks.get(vmid)}
    />
  );
}
