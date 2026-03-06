import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Network } from "lucide-react";
import { ClusterSelector } from "../components/ClusterSelector";
import { NetworkInterfaceTable } from "../components/NetworkInterfaceTable";
import { SDNTable } from "../components/SDNTable";

export function NetworksPage() {
  const [clusterId, setClusterId] = useState("");

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Network className="h-6 w-6 text-primary" />
          <h1 className="text-2xl font-semibold">Networks</h1>
        </div>
        <ClusterSelector value={clusterId} onChange={setClusterId} />
      </div>

      {clusterId.length === 0 ? (
        <p className="text-muted-foreground">
          Select a cluster to view network interfaces and SDN configuration.
        </p>
      ) : (
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
      )}
    </div>
  );
}
