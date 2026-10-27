import { useMemo } from "react";
import { apiPath } from "@/lib/api-path";
import { HistoryTimeline } from "@/features/history/components/HistoryTimeline";
import type { HistoryTarget } from "@/features/history/types/history";
import { guestLinksByVmid } from "@/features/vms/lib/guest-links";
import { useClusterVMs } from "../api/cluster-queries";
import { clusterHistoryKind } from "../lib/cluster-history-kind";

/**
 * A cluster's History tab: everything that happened in it, newest first —
 * every action recorded in it, done in Nexara or in Proxmox, its guests'
 * changes no task explains, and its sync failures as runs — with a Guest
 * column linking each guest's page.
 */
export function ClusterHistoryTab({ clusterId }: { clusterId: string }) {
  const { data: vms } = useClusterVMs(clusterId);
  const guestLinks = useMemo(
    () => guestLinksByVmid(vms, clusterId),
    [vms, clusterId],
  );
  const target = useMemo<HistoryTarget>(
    () => ({
      clusterId,
      path: (query) => apiPath`/api/v1/clusters/${clusterId}/history?${query}`,
    }),
    [clusterId],
  );
  return (
    <HistoryTimeline
      kind={clusterHistoryKind}
      target={target}
      guestLink={(vmid) => guestLinks.get(vmid)}
    />
  );
}
