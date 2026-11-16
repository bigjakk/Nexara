import { formatDateTime } from "@/lib/format";
import type { ReportRun, ReportSchedule } from "@/types/api";

/** The newest finished run: the one a reader can open. */
export function latestCompletedRun(
  runs: readonly ReportRun[] | undefined,
): ReportRun | undefined {
  let latest: ReportRun | undefined;
  for (const run of runs ?? []) {
    if (run.status !== "completed") continue;
    if (!latest || run.created_at > latest.created_at) latest = run;
  }
  return latest;
}

/**
 * The Reports page's one-line state: how many schedules there are, how many of
 * them are switched off, and how long ago a report was last generated. Until
 * either list has loaded it says what the page is for.
 */
export function reportsSummary(
  schedules: readonly ReportSchedule[] | undefined,
  runs: readonly ReportRun[] | undefined,
  ago: (iso: string) => string = formatDateTime,
): string {
  if (schedules === undefined || runs === undefined) {
    return "Generate a report now, or schedule one and have it emailed.";
  }
  const latest = latestCompletedRun(runs);
  if (schedules.length === 0 && latest === undefined) {
    return "No schedules and no reports yet. Generate a report now, or schedule one and have it emailed.";
  }
  const off = schedules.filter((s) => !s.enabled).length;
  const scheduled =
    schedules.length === 0
      ? "No schedules"
      : `${String(schedules.length)} ${schedules.length === 1 ? "schedule" : "schedules"}${off > 0 ? ` (${String(off)} off)` : ""}`;
  const last =
    latest === undefined
      ? "no report generated yet"
      : `last report generated ${ago(latest.created_at)}`;
  return `${scheduled}, ${last}`;
}
