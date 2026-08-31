import { VirtioWinConfigCard } from "./VirtioWinConfigCard";
import { VirtioWinDownloadsTable } from "./VirtioWinDownloadsTable";

interface ClusterGuestToolsTabProps {
  clusterId: string;
}

/**
 * Cluster-level guest tools management.
 *
 * Today this is the virtio-win ISO: making sure the media Windows guests need
 * is present on the cluster's storage. The in-guest half — detecting installed
 * versions and staging updates — lands here alongside it.
 */
export function ClusterGuestToolsTab({ clusterId }: ClusterGuestToolsTabProps) {
  return (
    <div className="space-y-4">
      <VirtioWinConfigCard clusterId={clusterId} />
      <VirtioWinDownloadsTable clusterId={clusterId} />
    </div>
  );
}
