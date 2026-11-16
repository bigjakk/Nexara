import {
  AlertTriangle,
  CheckCircle2,
  Circle,
  Loader2,
  XCircle,
} from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useVeeamSessions } from "../api/backup-queries";
import type { VeeamSession } from "../types/backup";
import { sessionResultLabel } from "./veeam-format";
import { latestRuns, runIsActive } from "./veeam-live-runs";
import { DateTime } from "@/components/DateTime";

function runIcon(session: VeeamSession) {
  if (runIsActive(session)) {
    return <Loader2 className="h-3.5 w-3.5 animate-spin text-info" />;
  }
  switch (session.result) {
    case "Success":
      return <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />;
    case "Warning":
      return <AlertTriangle className="h-3.5 w-3.5 text-amber-500" />;
    case "Failed":
      return <XCircle className="h-3.5 w-3.5 text-red-500" />;
  }
  return <Circle className="h-3.5 w-3.5 text-muted-foreground" />;
}

/**
 * A Veeam job's latest runs, as live rows at the top of its History: how each
 * ended, from the job's runs Nexara has collected (the server's Runs tab has
 * them all). The runs are not part of the history below, which is what was
 * done to the job: Veeam keeps tens of thousands of them, and a job that runs
 * every quarter hour would bury the changes.
 */
export function VeeamLiveRunRows({
  serverId,
  jobId,
  since,
  colSpan,
}: {
  serverId: string;
  jobId: string;
  /** Where the History's time filter looks back to (epoch milliseconds), or
   *  null for the whole history: older runs are left out, as older entries are. */
  since: number | null;
  /** The table's column count, which each live row spans. */
  colSpan: number;
}) {
  const { t } = useTranslation("history-veeam-job");
  const { data, isError } = useVeeamSessions(serverId, jobId);
  const runs = useMemo(() => latestRuns(data ?? [], since), [data, since]);

  if (isError) {
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
  if (runs.length === 0) return null;

  return (
    <>
      <tr className="border-b bg-foreground/[0.03]">
        <td
          colSpan={colSpan}
          className="px-4 pt-2 pb-1 text-xs text-muted-foreground"
        >
          <span className="font-medium">{t("live.title")}</span> ·{" "}
          {t("live.note")}
        </td>
      </tr>
      {runs.map((run) => {
        const label = sessionResultLabel(run);
        return (
          <tr
            key={run.id}
            data-testid="veeam-live-run"
            className="border-b bg-foreground/[0.03]"
          >
            <td colSpan={colSpan} className="px-4 py-1.5">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
                {runIcon(run)}
                {runIsActive(run) ? (
                  <span className="font-medium">{run.state}</span>
                ) : label !== null ? (
                  <span
                    className={
                      run.result === "Failed"
                        ? "font-medium text-red-700 dark:text-red-400"
                        : run.result === "Warning"
                          ? "font-medium text-amber-700 dark:text-amber-400"
                          : "font-medium"
                    }
                  >
                    {label}
                  </span>
                ) : null}
                {run.duration !== "" && (
                  <span className="font-mono text-xs text-muted-foreground">
                    {run.duration}
                  </span>
                )}
                <span className="ml-auto text-xs text-muted-foreground">
                  {run.initiated_by !== "" && `${run.initiated_by} · `}
                  <DateTime value={run.creation_time} />
                </span>
              </div>
            </td>
          </tr>
        );
      })}
    </>
  );
}
