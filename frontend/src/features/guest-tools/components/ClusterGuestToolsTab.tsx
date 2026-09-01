import { VirtioWinConfigCard } from "./VirtioWinConfigCard";
import { VirtioWinDownloadsTable } from "./VirtioWinDownloadsTable";
import { GuestToolsPolicyCard } from "./GuestToolsPolicyCard";
import { GuestToolsFleetTable } from "./GuestToolsFleetTable";

interface ClusterGuestToolsTabProps {
  clusterId: string;
}

/**
 * Cluster-level guest tools management, in the order an operator sets it up:
 * get the ISO onto storage, decide the update policy, then look at the fleet.
 */
export function ClusterGuestToolsTab({ clusterId }: ClusterGuestToolsTabProps) {
  return (
    <div className="space-y-4">
      <VirtioWinConfigCard clusterId={clusterId} />
      <GuestToolsPolicyCard clusterId={clusterId} />
      <GuestToolsFleetTable clusterId={clusterId} />
      <VirtioWinDownloadsTable clusterId={clusterId} />
    </div>
  );
}
