import { AlertTriangle, CheckCircle2, Loader2, XCircle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { formatAction } from "@/components/layout/activity-columns";
import { formatDateTime } from "@/lib/format";
import { historyActor } from "@/features/history/lib/history";
import type { HistoryEntry } from "@/features/history/types/history";
import {
  entryPbsUpid,
  pbsTaskOutcome,
  pbsWorkerId,
  type PBSTaskOutcome,
} from "../lib/pbs-live-tasks";
import type { PBSTask } from "../types/backup";

const OUTCOME_ICON: Record<PBSTaskOutcome, React.ReactNode> = {
  running: <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-500" />,
  ok: <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />,
  warning: <AlertTriangle className="h-3.5 w-3.5 text-amber-500" />,
  failed: <XCircle className="h-3.5 w-3.5 text-red-500" />,
};

/**
 * The Proxmox Backup Server's own latest tasks, as live rows at the top of its
 * History: what the server did — backups, garbage collections, verifications,
 * syncs, prunes — whoever started it, read from the server while the tab is
 * open. A task Nexara started is named by the user who started it, from the
 * entry that records it; that entry is left out of the page (PBSHistoryTab).
 */
export function PBSLiveTaskRows({
  tasks,
  failed,
  entries,
  colSpan,
}: {
  tasks: readonly PBSTask[];
  /** The task list could not be read. */
  failed: boolean;
  /** The page's entries, which name the tasks Nexara started. */
  entries: readonly HistoryEntry[];
  /** The table's column count, which each live row spans. */
  colSpan: number;
}) {
  const { t } = useTranslation("history-pbs");
  if (failed) {
    return (
      <tr className="border-b">
        <td
          colSpan={colSpan}
          className="px-4 py-2 text-xs text-muted-foreground"
        >
          {t("live.error")}
        </td>
      </tr>
    );
  }
  if (tasks.length === 0) return null;

  const startedHere = new Map<string, HistoryEntry>();
  for (const entry of entries) {
    const upid = entryPbsUpid(entry);
    if (upid !== "") startedHere.set(upid, entry);
  }

  return (
    <>
      <tr className="border-b bg-blue-500/5">
        <td
          colSpan={colSpan}
          className="px-4 pt-2 pb-1 text-xs text-muted-foreground"
        >
          <span className="font-medium">{t("live.title")}</span> ·{" "}
          {t("live.note")}
        </td>
      </tr>
      {tasks.map((task) => {
        const outcome = pbsTaskOutcome(task);
        const trigger = startedHere.get(task.upid);
        const worker = pbsWorkerId(task.upid);
        return (
          <tr
            key={task.upid}
            data-testid="pbs-live-task"
            className="border-b bg-blue-500/5"
          >
            <td colSpan={colSpan} className="px-4 py-1.5">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
                {OUTCOME_ICON[outcome]}
                <span className="font-medium">
                  {t(`live.workers.${task.worker_type}`, {
                    defaultValue: formatAction(task.worker_type),
                  })}
                </span>
                {worker !== "" && (
                  <span className="font-mono text-xs text-muted-foreground">
                    {worker}
                  </span>
                )}
                {outcome === "failed" && (
                  <span className="text-xs text-red-500">{task.status}</span>
                )}
                <span className="ml-auto text-xs text-muted-foreground">
                  {trigger
                    ? t("live.startedInNexara", { user: historyActor(trigger) })
                    : task.user}
                  {" · "}
                  {formatDateTime(
                    new Date(task.starttime * 1000).toISOString(),
                  )}
                </span>
              </div>
            </td>
          </tr>
        );
      })}
    </>
  );
}
