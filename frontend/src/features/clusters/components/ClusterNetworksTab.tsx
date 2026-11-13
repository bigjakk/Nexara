import { useState } from "react";
import { Segmented } from "@/components/ui/segmented";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import { NetworkInterfaceTable } from "@/features/networks/components/NetworkInterfaceTable";
import { SDNTable } from "@/features/networks/components/SDNTable";
import { HistoryTabContent } from "@/features/history/components/HistoryTab";
import { useCanViewHistory } from "@/features/history/hooks/useCanViewHistory";
import { clusterHistories } from "@/features/history/lib/cluster-histories";

interface ClusterNetworksTabProps {
  clusterId: string;
}

export function ClusterNetworksTab({ clusterId }: ClusterNetworksTabProps) {
  // The panels are Radix tab panels, so only the view on show is mounted and
  // reads its data; the switch above them is what picks it.
  const [view, setView] = useState("interfaces");
  const history = clusterHistories.networks(clusterId);
  const canHistory = useCanViewHistory(
    history.permission,
    history.target.clusterId === null,
  );
  const shown = view === "history" && !canHistory ? "interfaces" : view;

  return (
    <Tabs value={shown} onValueChange={setView}>
      <Segmented
        aria-label="Networks view"
        size="md"
        value={shown}
        onValueChange={setView}
        options={[
          { value: "interfaces", label: "Interfaces" },
          { value: "sdn", label: "SDN" },
          ...(canHistory ? [{ value: "history", label: "History" }] : []),
        ]}
      />
      <TabsContent
        value="interfaces"
        aria-label="Interfaces"
        aria-labelledby={undefined}
      >
        <NetworkInterfaceTable clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="sdn" aria-label="SDN" aria-labelledby={undefined}>
        <SDNTable clusterId={clusterId} />
      </TabsContent>
      <HistoryTabContent history={history} label="History" />
    </Tabs>
  );
}
