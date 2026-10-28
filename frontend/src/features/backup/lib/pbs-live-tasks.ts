import { parseDetails } from "@/components/layout/task-status";
import type { HistoryEntry } from "@/features/history/types/history";
import type { PBSTask } from "../types/backup";

/** How many of the server's latest tasks its History lists above the first page. */
export const LIVE_TASK_COUNT = 10;

/** The server's latest tasks, newest first. */
export function latestTasks(
  tasks: readonly PBSTask[],
  count = LIVE_TASK_COUNT,
): PBSTask[] {
  return [...tasks].sort((a, b) => b.starttime - a.starttime).slice(0, count);
}

/** The tasks that began within the time filter: from `since` (epoch
 *  milliseconds), or any time for null. */
export function tasksSince(
  tasks: readonly PBSTask[],
  since: number | null,
): PBSTask[] {
  return since === null
    ? [...tasks]
    : tasks.filter((task) => task.starttime * 1000 >= since);
}

/**
 * The worker id a PBS task's UPID names — a datastore, or a datastore and what
 * it ran on: field 7 of UPID:node:pid:pstart:task:start:type:id:user:, whose
 * \xNN escapes are decoded.
 */
export function pbsWorkerId(upid: string): string {
  return (upid.split(":")[7] ?? "").replace(/\\x([0-9a-fA-F]{2})/g, (_, hex) =>
    String.fromCharCode(parseInt(String(hex), 16)),
  );
}

/** The PBS task an entry records Nexara starting (details.pbs_upid), or "". */
export function entryPbsUpid(entry: HistoryEntry): string {
  const upid = parseDetails(entry.details)["pbs_upid"];
  return typeof upid === "string" ? upid : "";
}

export type PBSTaskOutcome = "running" | "ok" | "warning" | "failed";

/** How a PBS task ended: it has no end while it runs, and PBS words a finish
 *  with warnings "WARNINGS: n" and a failure by its error. */
export function pbsTaskOutcome(task: PBSTask): PBSTaskOutcome {
  if (!task.endtime) return "running";
  const status = task.status ?? "";
  if (status === "" || status === "OK") return "ok";
  return status.startsWith("WARNINGS") ? "warning" : "failed";
}
