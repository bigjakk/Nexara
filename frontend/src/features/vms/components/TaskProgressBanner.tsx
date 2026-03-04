import { useEffect, useRef } from "react";
import { Loader2, CheckCircle2, XCircle } from "lucide-react";
import { useTaskStatus } from "../api/vm-queries";
import type { ResourceKind } from "../types/vm";

interface TaskProgressBannerProps {
  clusterId: string;
  upid: string | null;
  kind: ResourceKind;
  resourceId: string;
  onComplete?: () => void;
}

export function TaskProgressBanner({
  clusterId,
  upid,
  kind: _kind,
  resourceId: _resourceId,
  onComplete,
}: TaskProgressBannerProps) {
  const { data: task } = useTaskStatus(clusterId, upid);

  const isStopped = task?.status === "stopped";
  const isOk = isStopped && task.exit_status === "OK";
  const isFailed = isStopped && task.exit_status !== "OK";

  // Track whether we've already fired onComplete for this UPID to avoid
  // calling it on every re-render while isStopped remains true.
  const firedRef = useRef<string | null>(null);

  useEffect(() => {
    if (isStopped && upid && firedRef.current !== upid) {
      firedRef.current = upid;
      // Let the parent (VMActions) handle the optimistic cache update.
      // Don't invalidate queries here — the DB hasn't been updated yet
      // and refetching would overwrite the optimistic status.
      onComplete?.();
    }
  }, [isStopped, upid, onComplete]);

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
