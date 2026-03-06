import { useEffect, useRef, useCallback } from "react";
import { Loader2, CheckCircle2, XCircle } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { useTaskStatus } from "../api/vm-queries";
import type { ResourceKind } from "../types/vm";

interface TaskProgressBannerProps {
  clusterId: string;
  upid: string | null;
  kind: ResourceKind;
  resourceId: string;
  onComplete?: () => void;
  description?: string;
}

export function TaskProgressBanner({
  clusterId,
  upid,
  kind: _kind,
  resourceId: _resourceId,
  onComplete,
  description,
}: TaskProgressBannerProps) {
  const { data: task } = useTaskStatus(clusterId, upid);
  const queryClient = useQueryClient();

  const isStopped = task?.status === "stopped";
  const isOk =
    isStopped &&
    (task.exit_status === "OK" ||
      task.exit_status === "" ||
      task.exit_status.startsWith("WARNINGS"));
  const isFailed = isStopped && !isOk;

  // Track whether we've already fired onComplete for this UPID.
  const firedRef = useRef<string | null>(null);
  // Track whether we've already persisted this UPID to the task history.
  const persistedRef = useRef<string | null>(null);
  // Track the last update key to avoid duplicate updates.
  const lastUpdateRef = useRef<string | null>(null);

  const invalidateAll = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: ["task-history"] });
    // Refresh cluster VMs/containers so the inventory tree updates immediately.
    void queryClient.invalidateQueries({ queryKey: ["clusters", clusterId, "vms"] });
    void queryClient.invalidateQueries({ queryKey: ["clusters", clusterId, "containers"] });
  }, [queryClient, clusterId]);

  // Persist task to DB when UPID becomes available.
  useEffect(() => {
    if (upid && persistedRef.current !== upid) {
      persistedRef.current = upid;
      void apiClient
        .post("/api/v1/tasks", {
          cluster_id: clusterId,
          upid,
          description: description ?? "Task",
          status: "running",
          node: "",
          task_type: "",
        })
        .then(invalidateAll)
        .catch(() => {
          // ignore — task may already exist (ON CONFLICT)
        });
    }
  }, [upid, clusterId, description, invalidateAll]);

  // Update task status on each poll result.
  useEffect(() => {
    if (!upid || !task) return;
    const key = `${upid}:${task.status}:${task.exit_status ?? ""}:${String(task.progress)}`;
    if (lastUpdateRef.current === key) return;
    lastUpdateRef.current = key;

    void apiClient
      .put(`/api/v1/tasks/${encodeURIComponent(upid)}`, {
        status: task.status,
        exit_status: task.exit_status ?? "",
        progress: task.progress ?? null,
        finished_at: task.status === "stopped" ? new Date().toISOString() : null,
      })
      .then(invalidateAll)
      .catch(() => {
        // ignore update failures
      });
  }, [upid, task, invalidateAll]);

  useEffect(() => {
    if (isStopped && upid && task && firedRef.current !== upid) {
      firedRef.current = upid;
      // Ensure the final status is persisted to DB before firing onComplete,
      // which may unmount this component.
      void apiClient
        .put(`/api/v1/tasks/${encodeURIComponent(upid)}`, {
          status: task.status,
          exit_status: task.exit_status ?? "",
          progress: task.progress ?? null,
          finished_at: new Date().toISOString(),
        })
        .then(invalidateAll)
        .catch(() => {
          // ignore
        })
        .finally(() => {
          onComplete?.();
        });
    }
  }, [isStopped, upid, task, onComplete, invalidateAll]);

  if (!upid) return null;

  return (
    <div className="flex items-center gap-2 rounded-md border px-3 py-2 text-sm">
      {!isStopped && (
        <>
          <Loader2 className="h-4 w-4 animate-spin text-blue-500" />
          <div className="flex flex-1 items-center gap-2">
            <span>
              Task running
              {task?.progress != null
                ? `… ${Math.round(task.progress * 100)}%`
                : "…"}
            </span>
            <span className="text-xs text-muted-foreground">{task?.type}</span>
            {task?.progress != null && (
              <div className="ml-auto h-2 w-32 overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full rounded-full bg-blue-500 transition-all duration-300"
                  style={{ width: `${Math.round(task.progress * 100)}%` }}
                />
              </div>
            )}
          </div>
        </>
      )}
      {isOk && (
        <>
          <CheckCircle2 className="h-4 w-4 text-green-500" />
          <span>Task completed successfully</span>
        </>
      )}
      {isFailed && (
        <>
          <XCircle className="h-4 w-4 text-red-500" />
          <span>
            Task failed{task.exit_status ? `: ${task.exit_status}` : ""}
          </span>
        </>
      )}
    </div>
  );
}
