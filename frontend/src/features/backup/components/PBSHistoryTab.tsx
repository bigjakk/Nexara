import { useMemo } from "react";
import { TabsContent, TabsTrigger } from "@/components/ui/tabs";
import { HistoryTimeline } from "@/features/history/components/HistoryTimeline";
import { useCanViewHistoryOf } from "@/features/history/hooks/useCanViewHistoryOf";
import { backupHistories } from "@/features/history/lib/backup-histories";
import { usePBSTasks } from "../api/backup-queries";
import { entryPbsUpid, latestTasks } from "../lib/pbs-live-tasks";
import { PBSLiveTaskRows } from "./PBSLiveTaskRows";

/** The History tab's trigger, shown with the grants the History takes. */
export function PBSHistoryTabTrigger() {
  const can = useCanViewHistoryOf("pbs");
  return can ? <TabsTrigger value="history">History</TabsTrigger> : null;
}

/**
 * A Proxmox Backup Server's History: what was done to it in Nexara — its
 * configuration, datastore maintenance, jobs run, restores from it — and, above
 * the first page, the server's own latest tasks, live. A task Nexara started is
 * one row, named by who started it: its entry is the page's, and the live row
 * stands for it while the server still lists the task.
 */
export function PBSHistoryTabContent({ pbsId }: { pbsId: string }) {
  const can = useCanViewHistoryOf("pbs");
  const history = useMemo(() => backupHistories.pbsServer(pbsId), [pbsId]);
  const { data, isError } = usePBSTasks(pbsId);
  const latest = useMemo(() => latestTasks(data ?? []), [data]);
  const listed = useMemo(
    () => new Set(latest.map((task) => task.upid)),
    [latest],
  );
  if (!can) return null;
  return (
    <TabsContent value="history" className="mt-4">
      <HistoryTimeline
        kind={history.kind}
        target={history.target}
        rowsAbove={(colSpan, entries) => (
          <PBSLiveTaskRows
            tasks={latest}
            failed={isError}
            entries={entries}
            colSpan={colSpan}
          />
        )}
        absorbs={(entry) => listed.has(entryPbsUpid(entry))}
      />
    </TabsContent>
  );
}
