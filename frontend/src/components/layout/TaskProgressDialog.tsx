import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Loader2, CheckCircle2, XCircle } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { apiClient } from "@/lib/api-client";
import { useTaskStatus } from "@/features/vms/api/vm-queries";
import type { TaskLogLine } from "@/features/vms/api/vm-queries";
import { useTaskLogStore } from "@/stores/task-log-store";

/**
 * Global task progress dialog driven by `focusedTask` in the task-log store.
 * Shows live progress bar, status, speed extraction, and streaming task log.
 */
export function TaskProgressDialog() {
  const { t } = useTranslation("common");
  const focusedTask = useTaskLogStore((s) => s.focusedTask);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  const clusterId = focusedTask?.clusterId ?? "";
  const upid = focusedTask?.upid ?? "";
  const description = focusedTask?.description ?? "Task";

  // Poll Proxmox task status (includes progress extracted from log)
  const { data: task } = useTaskStatus(clusterId, upid || null);
  const queryClient = useQueryClient();

  const isActive = !!upid && (!task || task.status !== "stopped");
  const isOk =
    task?.status === "stopped" &&
    (task.exit_status === "OK" ||
      task.exit_status === "" ||
      task.exit_status.startsWith("WARNINGS"));
  const isFailed = task?.status === "stopped" && !isOk;

  // Fetch task log with live polling — separate from the stale useTaskLog cache
  const { data: logLines } = useQuery({
    queryKey: ["task-progress-dialog-log", clusterId, upid],
    queryFn: () =>
      apiClient.get<TaskLogLine[]>(
        `/api/v1/clusters/${clusterId}/tasks/${encodeURIComponent(upid)}/log`,
      ),
    enabled: !!upid && clusterId.length > 0,
    refetchInterval: isActive ? 2000 : false,
    staleTime: 0,
  });

  // Auto-scroll the log container
  const logRef = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight;
    }
  }, [logLines]);

  // Invalidate caches when task finishes
  const prevStatus = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (
      task?.status &&
      prevStatus.current !== task.status &&
      task.status === "stopped"
    ) {
      void queryClient.invalidateQueries({ queryKey: ["recent-activity"] });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId],
      });
    }
    prevStatus.current = task?.status;
  }, [task?.status, clusterId, queryClient]);

  // Extract log text
  const allLogText = logLines?.map((l) => l.t) ?? [];

  // Extract speed/throughput lines
  const speedLines = allLogText.filter((t) =>
    /\b(MiB\/s|GiB\/s|KiB\/s|MB\/s|GB\/s|transferred)\b/i.test(t),
  );
  const speedLine =
    speedLines.length > 0 ? speedLines[speedLines.length - 1] : undefined;

  const progressPct =
    task?.progress != null ? Math.round(task.progress * 100) : null;

  return (
    <Dialog
      open={focusedTask !== null}
      onOpenChange={(v) => {
        if (!v) setFocusedTask(null);
      }}
    >
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-sm">
            {isActive && (
              <Loader2 className="h-4 w-4 animate-spin text-blue-500" />
            )}
            {isOk && <CheckCircle2 className="h-4 w-4 text-green-500" />}
            {isFailed && <XCircle className="h-4 w-4 text-red-500" />}
            {description}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          {/* Status */}
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium">{t("status")}</span>
            <Badge
              variant="outline"
              className={
                isActive
                  ? "bg-blue-100 text-blue-700"
                  : isOk
                    ? "bg-green-100 text-green-700"
                    : "bg-red-100 text-red-700"
              }
            >
              {isActive ? t("running") : isOk ? t("completed") : t("failed")}
            </Badge>
          </div>

          {/* Progress bar — show when active or has progress data */}
          {(isActive || (progressPct != null && progressPct < 100)) && (
            <div className="space-y-2">
              <div className="h-2 overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full bg-primary transition-all duration-300"
                  style={{ width: `${String(progressPct ?? 2)}%` }}
                />
              </div>
              <div className="flex items-center justify-between">
                <p className="text-xs text-muted-foreground">
                  {progressPct != null
                    ? `${String(progressPct)}%`
                    : t("inProgress")}
                </p>
                {speedLine && (
                  <p className="break-all text-xs font-medium text-blue-600 dark:text-blue-400">
                    {speedLine}
                  </p>
                )}
              </div>
            </div>
          )}

          {/* Completion messages */}
          {isOk && (
            <p className="text-sm text-green-600">
              {t("taskCompletedSuccessfully")}
            </p>
          )}
          {isFailed && (
            <p className="text-sm text-red-500">
              {t("taskFailed")}{task.exit_status ? `: ${task.exit_status}` : ""}
            </p>
          )}

          {/* Task Log */}
          {allLogText.length > 0 && (
            <div className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">
                {t("taskLog")}
              </span>
              <pre
                ref={logRef}
                className="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/50 p-2 font-mono text-[11px] leading-relaxed"
              >
                {allLogText.join("\n")}
              </pre>
            </div>
          )}

          {allLogText.length === 0 && isActive && (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="h-3 w-3 animate-spin" />
              {t("waitingForTaskLog")}
            </div>
          )}

          {/* Close button */}
          <Button
            variant="outline"
            onClick={() => {
              setFocusedTask(null);
            }}
            className="w-full"
          >
            {isActive ? t("closeTaskBackground") : t("close")}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
