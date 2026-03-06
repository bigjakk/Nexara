import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { NetworkInterfaceTable } from "@/features/networks/components/NetworkInterfaceTable";
import { SDNTable } from "@/features/networks/components/SDNTable";

interface ClusterNetworksTabProps {
  clusterId: string;
}

export function ClusterNetworksTab({ clusterId }: ClusterNetworksTabProps) {
  return (
    <Tabs defaultValue="interfaces">
      <TabsList>
        <TabsTrigger value="interfaces">Interfaces</TabsTrigger>
        <TabsTrigger value="sdn">SDN</TabsTrigger>
      </TabsList>
      <TabsContent value="interfaces" className="mt-4">
        <NetworkInterfaceTable clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="sdn" className="mt-4">
        <SDNTable clusterId={clusterId} />
      </TabsContent>
    </Tabs>
  );
}
