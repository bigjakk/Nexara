import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { NetworkInterfaceTable } from "@/features/networks/components/NetworkInterfaceTable";
import { SDNTable } from "@/features/networks/components/SDNTable";
import {
  HistoryTabContent,
  HistoryTabTrigger,
} from "@/features/history/components/HistoryTab";
import { clusterHistories } from "@/features/history/lib/cluster-histories";

interface ClusterNetworksTabProps {
  clusterId: string;
}

export function ClusterNetworksTab({ clusterId }: ClusterNetworksTabProps) {
  return (
    <Tabs defaultValue="interfaces">
      <TabsList>
        <TabsTrigger value="interfaces">Interfaces</TabsTrigger>
        <TabsTrigger value="sdn">SDN</TabsTrigger>
        <HistoryTabTrigger />
      </TabsList>
      <TabsContent value="interfaces" className="mt-4">
        <NetworkInterfaceTable clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="sdn" className="mt-4">
        <SDNTable clusterId={clusterId} />
      </TabsContent>
      <HistoryTabContent history={clusterHistories.networks(clusterId)} />
    </Tabs>
  );
}
