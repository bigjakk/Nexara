import { useCallback, useRef, useState } from "react";
import {
  ChevronUp,
  ChevronDown,
  ChevronRight,
  Loader2,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { useTaskLogStore } from "@/stores/task-log-store";
import {
  useTaskHistory,
  useClearTaskHistory,
  useTaskLog,
  type TaskHistoryEntry,
} from "@/features/vms/api/vm-queries";

function isTaskOk(exitStatus: string): boolean {
  return exitStatus === "" || exitStatus === "OK" || exitStatus.startsWith("WARNINGS");
}

function formatElapsed(startedAt: string, finishedAt: string | null): string {
  const start = new Date(startedAt).getTime();
  const end = finishedAt ? new Date(finishedAt).getTime() : Date.now();
  const seconds = Math.floor((end - start) / 1000);

  if (!finishedAt) {
    if (seconds < 60) return `${seconds}s`;
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    return `${m}m ${s}s`;
  }

  const ago = Math.floor((Date.now() - end) / 1000);
  if (ago < 60) return `${ago}s ago`;
  if (ago < 3600) return `${Math.floor(ago / 60)}m ago`;
  if (ago < 86400) return `${Math.floor(ago / 3600)}h ago`;
  return `${Math.floor(ago / 86400)}d ago`;
}

function formatTimestamp(iso: string): string {
  return new Date(iso).toLocaleString();
}

function formatDuration(startedAt: string, finishedAt: string | null): string {
  const start = new Date(startedAt).getTime();
  const end = finishedAt ? new Date(finishedAt).getTime() : Date.now();
  const seconds = Math.floor((end - start) / 1000);
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}m ${s}s`;
}

function TaskRow({ task, expanded, onToggle }: {
  task: TaskHistoryEntry;
  expanded: boolean;
  onToggle: () => void;
}) {
  const isRunning = task.status === "running";
  const isOk = task.status === "stopped" && isTaskOk(task.exit_status);
  const hasWarnings = isOk && task.exit_status.startsWith("WARNINGS");
  const isFailed = task.status === "stopped" && !isOk;

  const { data: logLines, isLoading: logLoading } = useTaskLog(
    task.cluster_id,
    task.upid,
    expanded,
  );

  return (
    <>
      <tr
        className="cursor-pointer border-b hover:bg-muted/20"
        onClick={onToggle}
      >
        <td className="px-2 py-1">
          <div className="flex items-center gap-1">
            <ChevronRight
              className={`h-3 w-3 text-muted-foreground transition-transform ${expanded ? "rotate-90" : ""}`}
            />
            {isRunning && (
              <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-500" />
            )}
            {isOk && !hasWarnings && (
              <CheckCircle2 className="h-3.5 w-3.5 text-green-500" />
            )}
            {hasWarnings && (
              <AlertTriangle className="h-3.5 w-3.5 text-yellow-500" />
            )}
            {isFailed && (
              <XCircle className="h-3.5 w-3.5 text-red-500" />
            )}
          </div>
        </td>
        <td className="px-2 py-1">
          <div className="flex items-center gap-2">
            <span>
              {task.description || task.task_type || "Task"}
            </span>
            {isRunning && task.progress != null && (
              <div className="h-1.5 w-20 overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full rounded-full bg-blue-500 transition-all duration-300"
                  style={{
                    width: `${Math.round(task.progress * 100)}%`,
                  }}
                />
              </div>
            )}
          </div>
        </td>
        <td className="px-2 py-1">
          {isFailed && (
            <span className="text-red-500">
              {task.exit_status}
            </span>
          )}
          {isOk && !hasWarnings && (
            <span className="text-green-600 dark:text-green-400">
              OK
            </span>
          )}
          {hasWarnings && (
            <span className="text-yellow-600 dark:text-yellow-400">
              {task.exit_status}
            </span>
          )}
          {isRunning && (
            <span className="text-blue-500">Running</span>
          )}
        </td>
        <td className="px-2 py-1 text-right text-muted-foreground">
          {formatElapsed(task.started_at, task.finished_at)}
        </td>
      </tr>
      {expanded && (
        <tr className="border-b bg-muted/10">
          <td colSpan={4} className="px-4 py-2">
            <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
              {task.exit_status !== "" && (
                <>
                  <span className="text-muted-foreground">Exit Status</span>
                  <span className={
                    isFailed
                      ? "text-red-500"
                      : hasWarnings
                        ? "text-yellow-600 dark:text-yellow-400"
                        : ""
                  }>
                    {task.exit_status}
                  </span>
                </>
              )}
              <span className="text-muted-foreground">UPID</span>
              <span className="break-all font-mono text-[10px]">{task.upid}</span>
              {task.node !== "" && (
                <>
                  <span className="text-muted-foreground">Node</span>
                  <span>{task.node}</span>
                </>
              )}
              {task.task_type !== "" && (
                <>
                  <span className="text-muted-foreground">Type</span>
                  <span>{task.task_type}</span>
                </>
              )}
              <span className="text-muted-foreground">Started</span>
              <span>{formatTimestamp(task.started_at)}</span>
              {task.finished_at && (
                <>
                  <span className="text-muted-foreground">Finished</span>
                  <span>{formatTimestamp(task.finished_at)}</span>
                </>
              )}
              <span className="text-muted-foreground">Duration</span>
              <span>{formatDuration(task.started_at, task.finished_at)}</span>
            </div>

            {/* Task Log Output */}
            <div className="mt-2 border-t pt-2">
              <span className="text-xs font-medium text-muted-foreground">Log</span>
              {logLoading && (
                <div className="mt-1 flex items-center gap-1 text-xs text-muted-foreground">
                  <Loader2 className="h-3 w-3 animate-spin" />
                  Loading log…
                </div>
              )}
              {logLines && logLines.length > 0 && (
                <pre className="mt-1 max-h-40 overflow-auto rounded bg-muted/50 p-2 font-mono text-[11px] leading-relaxed">
                  {logLines.map((line) => line.t).join("\n")}
                </pre>
              )}
              {logLines && logLines.length === 0 && (
                <div className="mt-1 text-xs text-muted-foreground">No log output</div>
              )}
            </div>
          </td>
        </tr>
      )}
    </>
  );
}

export function TaskLogPanel() {
  const panelOpen = useTaskLogStore((s) => s.panelOpen);
  const panelHeight = useTaskLogStore((s) => s.panelHeight);
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setPanelHeight = useTaskLogStore((s) => s.setPanelHeight);

  const { data: tasks } = useTaskHistory();
  const clearMutation = useClearTaskHistory();

  const [expandedId, setExpandedId] = useState<string | null>(null);

  const runningCount = tasks?.filter((t) => t.status === "running").length ?? 0;
  const failedCount =
    tasks?.filter(
      (t) => t.status === "stopped" && !isTaskOk(t.exit_status),
    ).length ?? 0;

  const dragRef = useRef<{ startY: number; startHeight: number } | null>(null);

  const handlePointerDown = useCallback(
    (e: React.PointerEvent) => {
      e.preventDefault();
      dragRef.current = { startY: e.clientY, startHeight: panelHeight };
      const el = e.currentTarget as HTMLElement;
      el.setPointerCapture(e.pointerId);
    },
    [panelHeight],
  );

  const handlePointerMove = useCallback(
    (e: React.PointerEvent) => {
      if (!dragRef.current) return;
      const delta = dragRef.current.startY - e.clientY;
      setPanelHeight(dragRef.current.startHeight + delta);
    },
    [setPanelHeight],
  );

  const handlePointerUp = useCallback(() => {
    dragRef.current = null;
  }, []);

  return (
    <div className="flex flex-col border-t bg-background">
      {/* Resize handle — only visible when panel is open */}
      {panelOpen && (
        <div
          className="h-1 cursor-row-resize bg-border hover:bg-primary/30"
          onPointerDown={handlePointerDown}
          onPointerMove={handlePointerMove}
          onPointerUp={handlePointerUp}
        />
      )}

      {/* Header bar */}
      <div
        className="flex h-7 cursor-pointer items-center gap-2 border-b px-3 text-xs select-none"
        onClick={() => { setPanelOpen(!panelOpen); }}
      >
        <span className="font-medium">Tasks</span>
        {runningCount > 0 && (
          <span className="rounded-full bg-blue-500/20 px-1.5 py-0.5 text-blue-600 dark:text-blue-400">
            {runningCount} running
          </span>
        )}
        {failedCount > 0 && (
          <span className="rounded-full bg-red-500/20 px-1.5 py-0.5 text-red-600 dark:text-red-400">
            {failedCount} failed
          </span>
        )}
        <div className="flex-1" />
        {panelOpen && tasks && tasks.length > 0 && (
          <Button
            variant="ghost"
            size="sm"
            className="h-5 px-1.5 text-xs"
            onClick={(e) => {
              e.stopPropagation();
              clearMutation.mutate();
            }}
          >
            <Trash2 className="mr-1 h-3 w-3" />
            Clear
          </Button>
        )}
        {panelOpen ? (
          <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
        ) : (
          <ChevronUp className="h-3.5 w-3.5 text-muted-foreground" />
        )}
      </div>

      {/* Task list */}
      {panelOpen && (
        <div
          className="overflow-auto"
          style={{ height: panelHeight }}
        >
          {(!tasks || tasks.length === 0) && (
            <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
              No tasks
            </div>
          )}
          {tasks && tasks.length > 0 && (
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b bg-muted/30 text-left">
                  <th className="w-12 px-2 py-1" />
                  <th className="px-2 py-1 font-medium">Description</th>
                  <th className="px-2 py-1 font-medium">Status</th>
                  <th className="w-24 px-2 py-1 text-right font-medium">Time</th>
                </tr>
              </thead>
              <tbody>
                {tasks.map((task) => (
                  <TaskRow
                    key={task.id}
                    task={task}
                    expanded={expandedId === task.id}
                    onToggle={() => {
                      setExpandedId(expandedId === task.id ? null : task.id);
                    }}
                  />
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
}
