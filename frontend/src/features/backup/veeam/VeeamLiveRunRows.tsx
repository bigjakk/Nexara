import {
  AlertTriangle,
  CheckCircle2,
  Circle,
  Loader2,
  XCircle,
} from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import { formatDateTime } from "@/lib/format";
import { useVeeamSessions } from "../api/backup-queries";
import type { VeeamSession } from "../types/backup";
import { resultVariant, sessionResultLabel } from "./veeam-format";
import { latestRuns, runIsActive } from "./veeam-live-runs";

function runIcon(session: VeeamSession) {
  if (runIsActive(session)) {
    return <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-500" />;
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
 * ended, from the runs Nexara has collected (the server's Runs tab has them
 * all). The runs are not part of the history below, which is what was done to
 * the job: Veeam keeps tens of thousands of them, and a job that runs every
 * quarter hour would bury the changes.
 */
export function VeeamLiveRunRows({
  serverId,
  jobId,
  colSpan,
}: {
  serverId: string;
  jobId: string;
  /** The table's column count, which each live row spans. */
  colSpan: number;
}) {
  const { t } = useTranslation("history-veeam-job");
  const { data, isError } = useVeeamSessions(serverId);
  const runs = useMemo(() => latestRuns(data ?? [], jobId), [data, jobId]);

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
      <tr className="border-b bg-blue-500/5">
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
            className="border-b bg-blue-500/5"
          >
            <td colSpan={colSpan} className="px-4 py-1.5">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
                {runIcon(run)}
                {runIsActive(run) ? (
                  <Badge variant="secondary">{run.state}</Badge>
                ) : label !== null ? (
                  <Badge variant={resultVariant(run.result)}>{label}</Badge>
                ) : null}
                {run.duration !== "" && (
                  <span className="font-mono text-xs text-muted-foreground">
                    {run.duration}
                  </span>
                )}
                <span className="ml-auto text-xs text-muted-foreground">
                  {run.initiated_by !== "" && `${run.initiated_by} · `}
                  {formatDateTime(run.creation_time)}
                </span>
              </div>
            </td>
          </tr>
        );
      })}
    </>
  );
}
