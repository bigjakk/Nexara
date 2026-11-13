import type { ReactNode } from "react";
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
  const can = useCanViewHistory(
    history.permission,
    history.target.clusterId === null,
  );
  return can ? <TabsTrigger value={value}>History</TabsTrigger> : null;
}

/**
 * The content of a tab strip's History trigger: a tab's own History, read once
 * its tab is open (Radix mounts only the open tab's content).
 */
export function HistoryTabContent({
  history,
  value = "history",
  placeholder,
  label,
}: {
  history: ClusterHistory;
  value?: string;
  /** Shown in place of the History while it cannot be read yet — or at all —
   *  because the object's page does not know where to read it: nothing is
   *  read meanwhile. */
  placeholder?: ReactNode;
  /** Names the panel where no HistoryTabTrigger does: a caller that switches
   *  views with something other than a tab strip. Radix would otherwise point
   *  aria-labelledby at a trigger that is not there. */
  label?: string;
}) {
  const can = useCanViewHistory(
    history.permission,
    history.target.clusterId === null,
  );
  return can ? (
    <TabsContent
      value={value}
      className="mt-4"
      {...(label === undefined
        ? {}
        : { "aria-label": label, "aria-labelledby": undefined })}
    >
      {placeholder ?? (
        <HistoryTimeline kind={history.kind} target={history.target} />
      )}
    </TabsContent>
  ) : null;
}
