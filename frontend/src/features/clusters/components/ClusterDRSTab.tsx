import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DRSConfigCard } from "@/features/drs/components/DRSConfigCard";
import { EvaluateButton } from "@/features/drs/components/EvaluateButton";
import { DRSRulesTable } from "@/features/drs/components/DRSRulesTable";
import { CreateRuleDialog } from "@/features/drs/components/CreateRuleDialog";
import { DRSHistoryTable } from "@/features/drs/components/DRSHistoryTable";

interface ClusterDRSTabProps {
  clusterId: string;
}

export function ClusterDRSTab({ clusterId }: ClusterDRSTabProps) {
  return (
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
  );
}
