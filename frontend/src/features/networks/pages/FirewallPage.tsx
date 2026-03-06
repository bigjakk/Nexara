import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Shield } from "lucide-react";
import { ClusterSelector } from "../components/ClusterSelector";
import { FirewallRulesTable } from "../components/FirewallRulesTable";
import { FirewallOptionsCard } from "../components/FirewallOptionsCard";
import { FirewallTemplatesTable } from "../components/FirewallTemplatesTable";

export function FirewallPage() {
  const [clusterId, setClusterId] = useState("");

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Shield className="h-6 w-6 text-primary" />
          <h1 className="text-2xl font-semibold">Firewall</h1>
        </div>
        <ClusterSelector value={clusterId} onChange={setClusterId} />
      </div>

      {clusterId.length === 0 ? (
        <p className="text-muted-foreground">
          Select a cluster to manage firewall rules and options.
        </p>
      ) : (
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
      )}
    </div>
  );
}
