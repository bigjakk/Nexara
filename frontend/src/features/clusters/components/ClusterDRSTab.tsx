import { useState } from "react";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import { Segmented } from "@/components/ui/segmented";
import { DRSConfigCard } from "@/features/drs/components/DRSConfigCard";
import { EvaluateButton } from "@/features/drs/components/EvaluateButton";
import { DRSRulesTable } from "@/features/drs/components/DRSRulesTable";
import { CreateRuleDialog } from "@/features/drs/components/CreateRuleDialog";
import { DRSHistoryTable } from "@/features/drs/components/DRSHistoryTable";
import { HistoryTabContent } from "@/features/history/components/HistoryTab";
import { useCanViewHistory } from "@/features/history/hooks/useCanViewHistory";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import { usePermissions } from "@/hooks/usePermissions";

interface ClusterDRSTabProps {
  clusterId: string;
}

/** The views of the tab, in the order the switch shows them. */
const DRS_VIEWS = [
  { value: "configuration", label: "Configuration" },
  { value: "rules", label: "Rules" },
  { value: "migrations", label: "Migrations" },
] as const;

export function ClusterDRSTab({ clusterId }: ClusterDRSTabProps) {
  // The panels are Radix tab panels, so only the view on show is mounted and
  // reads its data; the switch above them is what picks it.
  const [view, setView] = useState("configuration");
  const history = clusterHistories.drs(clusterId);
  const canHistory = useCanViewHistory(
    history.permission,
    history.target.clusterId === null,
  );
  const shown = view === "history" && !canHistory ? "configuration" : view;
  // Running an evaluation and creating or deleting a rule all take manage:drs.
  // The evaluation panel shows only what a run returns, so it goes too.
  const canManage = usePermissions().hasPermission("manage", "drs");

  return (
    <Tabs value={shown} onValueChange={setView}>
      <Segmented
        aria-label="DRS view"
        size="md"
        value={shown}
        onValueChange={setView}
        options={[
          ...DRS_VIEWS,
          ...(canHistory ? [{ value: "history", label: "History" }] : []),
        ]}
      />

      {/* The settings on the left, and beside them what they would do to
          the cluster as it stands. Folds into one column when narrow. */}
      <TabsContent
        value="configuration"
        aria-label="Configuration"
        aria-labelledby={undefined}
      >
        <div className="flex flex-wrap items-start gap-4">
          <div className="min-w-0 flex-[999_1_520px]">
            <DRSConfigCard clusterId={clusterId} />
          </div>
          {canManage && (
            <div className="min-w-0 flex-[1_1_340px]">
              <EvaluateButton clusterId={clusterId} />
            </div>
          )}
        </div>
      </TabsContent>

      <TabsContent value="rules" aria-label="Rules" aria-labelledby={undefined}>
        <DRSRulesTable
          clusterId={clusterId}
          canDelete={canManage}
          actions={
            canManage ? <CreateRuleDialog clusterId={clusterId} /> : undefined
          }
        />
      </TabsContent>

      <TabsContent
        value="migrations"
        aria-label="Migrations"
        aria-labelledby={undefined}
      >
        <DRSHistoryTable clusterId={clusterId} />
      </TabsContent>

      <HistoryTabContent history={history} label="History" />
    </Tabs>
  );
}
