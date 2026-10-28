import { useMemo } from "react";
import { TabsContent } from "@/components/ui/tabs";
import { HistoryTimeline } from "@/features/history/components/HistoryTimeline";
import { useCanViewHistory } from "@/features/history/hooks/useCanViewHistory";
import { backupHistories } from "@/features/history/lib/backup-histories";
import { usePBSTasks } from "../api/backup-queries";
import { entryPbsUpid, latestTasks } from "../lib/pbs-live-tasks";
import { PBSLiveTaskRows } from "./PBSLiveTaskRows";

/**
 * A Proxmox Backup Server's History: what was done to it in Nexara — its
 * configuration, datastore maintenance, jobs run, restores from it — and, above
 * the first page, the server's own latest tasks, live. A task Nexara started is
 * one row, named by who started it: its entry is the page's, and the live row
 * stands for it while the server still lists the task. A refresh that failed
 * leaves the last tasks read in the query, so the entries come back with the
 * block's "could not read": nothing stands for them.
 */
export function PBSHistoryTabContent({ pbsId }: { pbsId: string }) {
  const history = useMemo(() => backupHistories.pbsServer(pbsId), [pbsId]);
  const can = useCanViewHistory(history.permission);
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
        absorbs={
          isError ? undefined : (entry) => listed.has(entryPbsUpid(entry))
        }
      />
    </TabsContent>
  );
}
