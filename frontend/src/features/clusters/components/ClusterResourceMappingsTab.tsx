import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import { PCIMappingsCard } from "@/features/mappings/components/PCIMappingsCard";
import { USBMappingsCard } from "@/features/mappings/components/USBMappingsCard";

interface ClusterResourceMappingsTabProps {
  clusterId: string;
}

/**
 * The cluster's Proxmox resource mappings: the named host devices a VM can
 * pass through by mapping rather than by raw hardware address. A mapping is
 * the only way Nexara can: qemu-server lets nobody but root@pam set a raw
 * device, and Nexara connects with an API token.
 */
export function ClusterResourceMappingsTab({
  clusterId,
}: ClusterResourceMappingsTabProps) {
  return (
    <div className="space-y-4">
      <p className="flex flex-wrap items-center gap-x-1 text-sm text-muted-foreground">
        Named host devices a VM can pass through. A mapping is the only way
        Nexara can attach one.
        <ClusterHistoryButton
          history={clusterHistories.mappings(clusterId)}
          title="resource mappings"
        />
      </p>
      <USBMappingsCard clusterId={clusterId} />
      <PCIMappingsCard clusterId={clusterId} />
    </div>
  );
}
