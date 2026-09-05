import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TaskLogLine } from "@/features/vms/api/vm-queries";

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
}: {
  lines: TaskLogLine[] | undefined;
  isLoading?: boolean;
}) {
  const { t } = useTranslation("common");
  return (
    <div className="mt-2 border-t pt-2">
      <span className="text-xs font-medium text-muted-foreground">
        {t("log")}
      </span>
      {isLoading && (
        <div className="mt-1 flex items-center gap-1 text-xs text-muted-foreground">
          <Loader2 className="h-3 w-3 animate-spin" />
          {t("loadingLog")}
        </div>
      )}
      {lines && lines.length > 0 && (
        <pre className="mt-1 max-h-48 overflow-auto rounded bg-muted/50 p-2 font-mono text-[11px] leading-relaxed break-all whitespace-pre-wrap">
          {lines.map((line) => line.t).join("\n")}
        </pre>
      )}
      {lines && lines.length === 0 && (
        <div className="mt-1 text-xs text-muted-foreground">
          {t("noLogOutput")}
        </div>
      )}
    </div>
  );
}
