import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Brain } from "lucide-react";
import { ClusterSelector } from "../components/ClusterSelector";
import { DRSConfigCard } from "../components/DRSConfigCard";
import { EvaluateButton } from "../components/EvaluateButton";
import { DRSRulesTable } from "../components/DRSRulesTable";
import { CreateRuleDialog } from "../components/CreateRuleDialog";
import { DRSHistoryTable } from "../components/DRSHistoryTable";

export function DRSPage() {
  const [clusterId, setClusterId] = useState("");

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Brain className="h-6 w-6 text-primary" />
          <h1 className="text-2xl font-semibold">
            Distributed Resource Scheduler
          </h1>
        </div>
        <ClusterSelector value={clusterId} onChange={setClusterId} />
      </div>

      {clusterId.length === 0 ? (
        <p className="text-muted-foreground">
          Select a cluster to configure DRS.
        </p>
      ) : (
        <Tabs defaultValue="configuration">
          <TabsList>
            <TabsTrigger value="configuration">Configuration</TabsTrigger>
            <TabsTrigger value="rules">Rules</TabsTrigger>
            <TabsTrigger value="history">History</TabsTrigger>
          </TabsList>

          <TabsContent value="configuration" className="space-y-6">
            <DRSConfigCard clusterId={clusterId} />
            <EvaluateButton clusterId={clusterId} />
          </TabsContent>

          <TabsContent value="rules" className="space-y-4">
            <div className="flex justify-end">
              <CreateRuleDialog clusterId={clusterId} />
            </div>
            <DRSRulesTable clusterId={clusterId} />
          </TabsContent>

          <TabsContent value="history">
            <DRSHistoryTable clusterId={clusterId} />
          </TabsContent>
        </Tabs>
      )}
    </div>
  );
}
