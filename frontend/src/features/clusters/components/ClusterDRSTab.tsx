import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DRSConfigCard } from "@/features/drs/components/DRSConfigCard";
import { EvaluateButton } from "@/features/drs/components/EvaluateButton";
import { DRSRulesTable } from "@/features/drs/components/DRSRulesTable";
import { CreateRuleDialog } from "@/features/drs/components/CreateRuleDialog";
import { DRSHistoryTable } from "@/features/drs/components/DRSHistoryTable";
import {
  HistoryTabContent,
  HistoryTabTrigger,
} from "@/features/history/components/HistoryTab";
import { clusterHistories } from "@/features/history/lib/cluster-histories";

interface ClusterDRSTabProps {
  clusterId: string;
}

export function ClusterDRSTab({ clusterId }: ClusterDRSTabProps) {
  return (
    <Tabs defaultValue="configuration">
      <TabsList>
        <TabsTrigger value="configuration">Configuration</TabsTrigger>
        <TabsTrigger value="rules">Rules</TabsTrigger>
        <TabsTrigger value="migrations">Migrations</TabsTrigger>
        <HistoryTabTrigger history={clusterHistories.drs(clusterId)} />
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

      <TabsContent value="migrations">
        <DRSHistoryTable clusterId={clusterId} />
      </TabsContent>

      <HistoryTabContent history={clusterHistories.drs(clusterId)} />
    </Tabs>
  );
}
