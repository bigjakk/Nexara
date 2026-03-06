import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { FirewallRulesTable } from "@/features/networks/components/FirewallRulesTable";
import { FirewallOptionsCard } from "@/features/networks/components/FirewallOptionsCard";
import { FirewallTemplatesTable } from "@/features/networks/components/FirewallTemplatesTable";

interface ClusterFirewallTabProps {
  clusterId: string;
}

export function ClusterFirewallTab({ clusterId }: ClusterFirewallTabProps) {
  return (
    <Tabs defaultValue="rules">
      <TabsList>
        <TabsTrigger value="rules">Cluster Rules</TabsTrigger>
        <TabsTrigger value="templates">Templates</TabsTrigger>
        <TabsTrigger value="options">Options</TabsTrigger>
      </TabsList>
      <TabsContent value="rules" className="mt-4">
        <FirewallRulesTable clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="templates" className="mt-4">
        <FirewallTemplatesTable clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="options" className="mt-4">
        <FirewallOptionsCard clusterId={clusterId} />
      </TabsContent>
    </Tabs>
  );
}
