import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TaskLogLine } from "@/features/tasks/api/tasks-queries";

/**
 * Proxmox answers a log it can no longer open — rotated away — with this one
 * line rather than an error (PVE::Tools::dump_logfile), whatever start asked.
 */
function isMissingLog(lines: TaskLogLine[] | undefined): boolean {
  const only = lines?.length === 1 ? lines[0] : undefined;
  return only?.n === 1 && only.t.startsWith("unable to open file - ");
}

/**
 * The Log block under an expanded task: the heading, the spinner, the output,
 * and the distinct "Proxmox reported none" case.
 *
 * Takes the lines rather than fetching them. The three callers read from three
 * different queries — two share `useTaskLog`, the migration dialog polls its
 * own on a 3s interval while the job is live — but they render the same four
 * states, and it was the rendering that had drifted apart.
 *
 * Lines wrap rather than scroll sideways: every one of these sits in a
 * width-constrained container (a fixed-layout table cell, a bottom drawer, a
 * dialog), and a horizontal scrollbar there hides output instead of showing
 * it.
 */
export function TaskLogSection({
  lines,
  isLoading = false,
  isError = false,
}: {
  lines: TaskLogLine[] | undefined;
  isLoading?: boolean;
  /** The read failed. Proxmox rotates old task logs away, so a guest's
   *  History reaches tasks whose log is gone; say so rather than show nothing. */
  isError?: boolean;
}) {
  const { t } = useTranslation("common");
  const missing = isMissingLog(lines);
  return (
    <div className="mt-3">
      <span className="text-xs text-muted-foreground">{t("log")}</span>
      {isLoading && (
        <div className="mt-1 flex items-center gap-1.5 text-[12.5px] text-muted-foreground">
          <Loader2 className="h-3 w-3 animate-spin" />
          {t("loadingLog")}
        </div>
      )}
      {lines && lines.length > 0 && !missing && (
        // Recessed, not boxed: the output is read in place, as on a terminal.
        <pre className="mt-1 max-h-48 overflow-auto rounded-[7px] bg-foreground/[0.06] px-3 py-2 font-mono text-[11.5px] leading-[1.6] break-all whitespace-pre-wrap dark:bg-black/30">
          {lines.map((line) => line.t).join("\n")}
        </pre>
      )}
      {lines && lines.length === 0 && (
        <div className="mt-1 text-[12.5px] text-muted-foreground">
          {t("noLogOutput")}
        </div>
      )}
      {(missing || (isError && !isLoading && !lines)) && (
        <div className="mt-1 text-[12.5px] text-muted-foreground">
          {t("logUnavailable")}
        </div>
      )}
    </div>
  );
}
