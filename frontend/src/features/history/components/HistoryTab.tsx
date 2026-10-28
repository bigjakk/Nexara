import { TabsContent, TabsTrigger } from "@/components/ui/tabs";
import { useCanViewHistory } from "../hooks/useCanViewHistory";
import type { ClusterHistory } from "../lib/cluster-histories";
import { HistoryTimeline } from "./HistoryTimeline";

/**
 * A tab strip's History trigger: shown with the grants a History takes, and
 * the tab's own view permission.
 */
export function HistoryTabTrigger({
  history,
  value = "history",
}: {
  history: ClusterHistory;
  value?: string;
}) {
  const can = useCanViewHistory(history.permission);
  return can ? <TabsTrigger value={value}>History</TabsTrigger> : null;
}

/**
 * The content of a tab strip's History trigger: a tab's own History, read once
 * its tab is open (Radix mounts only the open tab's content).
 */
export function HistoryTabContent({
  history,
  value = "history",
}: {
  history: ClusterHistory;
  value?: string;
}) {
  const can = useCanViewHistory(history.permission);
  return can ? (
    <TabsContent value={value} className="mt-4">
      <HistoryTimeline kind={history.kind} target={history.target} />
    </TabsContent>
  ) : null;
}
