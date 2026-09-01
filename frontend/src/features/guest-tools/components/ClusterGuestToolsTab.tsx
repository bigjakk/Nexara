import { VirtioWinConfigCard } from "./VirtioWinConfigCard";
import { VirtioWinSourceCard } from "./VirtioWinSourceCard";
import { VirtioWinDownloadsTable } from "./VirtioWinDownloadsTable";
import { GuestToolsPolicyCard } from "./GuestToolsPolicyCard";
import { GuestToolsFleetTable } from "./GuestToolsFleetTable";

interface ClusterGuestToolsTabProps {
  clusterId: string;
}

/**
 * Cluster-level guest tools management, in the order an operator sets it up:
 * get the ISO onto storage, decide the update policy, then look at the fleet.
 *
 * The download source sits last because it is the only instance-wide control
 * here and most installs never touch it — but it is on this page rather than
 * under Administration because this is where an unreachable source shows up.
 */
export function ClusterGuestToolsTab({ clusterId }: ClusterGuestToolsTabProps) {
  return (
    <div className="space-y-4">
      <VirtioWinConfigCard clusterId={clusterId} />
      <GuestToolsPolicyCard clusterId={clusterId} />
      <GuestToolsFleetTable clusterId={clusterId} />
      <VirtioWinDownloadsTable clusterId={clusterId} />
      <VirtioWinSourceCard />
    </div>
  );
}
