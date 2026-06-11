import { useState } from "react";
import {
  ChevronLeft,
  ChevronRight,
  ChevronDown,
  Loader2,
  CheckCircle2,
  XCircle,
  Activity,
  Monitor,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useTaskStatus, useTaskLog } from "@/features/vms/api/vm-queries";
import { isOkExit } from "@/components/layout/task-status";
import { useTaskLogStore } from "@/stores/task-log-store";
import { useTasks, type TaskRecord } from "../api/tasks-queries";

const PAGE_SIZE = 50;

const selectClass =
  "flex h-9 w-[200px] rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

const statusFilters = [
  { value: "", label: "All Statuses" },
  { value: "running", label: "Running" },
  { value: "completed", label: "Completed" },
  { value: "failed", label: "Failed" },
  { value: "stopped", label: "Stopped" },
] as const;

type DisplayStatus = "running" | "ok" | "failed";

/**
 * Resolve the display status. The reconciled task_history status is
 * authoritative once terminal (preserves the d86b7df fix); only a row the
 * server still reports as running is refined by the live poll, so it flips to
 * done before the next reconcile tick.
 */
function deriveDisplayStatus(
  task: TaskRecord,
  live: { status: string; exit_status: string } | undefined,
): DisplayStatus {
  switch (task.status) {
    case "completed":
      return "ok";
    case "failed":
      return "failed";
    case "stopped":
      return isOkExit(task.exit_status) ? "ok" : "failed";
    case "running":
      if (live && live.status === "stopped") {
        return isOkExit(live.exit_status) ? "ok" : "failed";
      }
      return "running";
    default:
      return isOkExit(task.exit_status) ? "ok" : "failed";
  }
}

function StatusIcon({ status }: { status: DisplayStatus }) {
  if (status === "running")
    return <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-500" />;
  if (status === "ok")
    return <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />;
  return <XCircle className="h-3.5 w-3.5 text-red-500" />;
}

const STATUS_BADGE: Record<DisplayStatus, string> = {
  running: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  ok: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  failed: "bg-red-500/15 text-red-600 dark:text-red-400",
};
const STATUS_LABEL: Record<DisplayStatus, string> = {
  running: "Running",
  ok: "Completed",
  failed: "Failed",
};

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString();
}

function formatDuration(start: string, end: string | null): string {
  const startMs = new Date(start).getTime();
  const endMs = end ? new Date(end).getTime() : Date.now();
  const sec = Math.max(0, Math.round((endMs - startMs) / 1000));
  if (sec < 60) return `${String(sec)}s`;
  if (sec < 3600)
    return `${String(Math.floor(sec / 60))}m ${String(sec % 60)}s`;
  return `${String(Math.floor(sec / 3600))}h ${String(Math.floor((sec % 3600) / 60))}m`;
}

function TaskRow({
  task,
  clusterName,
  expanded,
  onToggle,
}: {
  task: TaskRecord;
  clusterName: string;
  expanded: boolean;
  onToggle: () => void;
}) {
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  const isRunning = task.status === "running";

  // Live-poll ONLY rows the server still reports as running. useTaskStatus
  // self-stops once the task is stopped, so the polled set is tiny.
  const { data: live } = useTaskStatus(
    task.cluster_id,
    isRunning ? task.upid : null,
  );

  const display = deriveDisplayStatus(task, live);
  const progress = display === "running" ? live?.progress : undefined;
  const exitText = task.exit_status || live?.exit_status || "";

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
        <td className="px-4 py-2 whitespace-nowrap text-muted-foreground">
          <div className="flex items-center gap-1.5">
            <ChevronDown
              className={`h-3 w-3 text-muted-foreground transition-transform ${expanded ? "" : "-rotate-90"}`}
            />
            <StatusIcon status={display} />
            {formatTime(task.started_at)}
          </div>
        </td>
        <td className="px-4 py-2">{clusterName}</td>
        <td className="px-4 py-2 font-mono text-xs">{task.task_type || "—"}</td>
        <td className="px-4 py-2">
          <div className="flex items-center gap-2">
            {task.source === "proxmox" && (
              <span className="inline-flex items-center gap-0.5 rounded-full bg-orange-500/10 px-1.5 py-0.5 text-[10px] font-medium leading-none text-orange-600 dark:text-orange-400">
                <Monitor className="h-2.5 w-2.5" />
                PVE
              </span>
            )}
            <span>{task.description || task.upid}</span>
            {display === "running" && progress != null && (
              <div className="flex items-center gap-1.5">
                <div className="h-1.5 w-24 overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full bg-blue-500 transition-all duration-500"
                    style={{ width: `${String(Math.round(progress * 100))}%` }}
                  />
                </div>
                <span className="text-[10px] tabular-nums text-blue-500">
                  {Math.round(progress * 100)}%
                </span>
              </div>
            )}
          </div>
        </td>
        <td className="px-4 py-2 text-muted-foreground">{task.node || "—"}</td>
        <td className="px-4 py-2">
          <span
            className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_BADGE[display]}`}
          >
            {STATUS_LABEL[display]}
          </span>
        </td>
      </tr>
      {expanded && (
        <tr className="border-b bg-muted/10">
          <td colSpan={6} className="px-4 py-3">
            <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
              <span className="text-muted-foreground">Description</span>
              <span>{task.description || "—"}</span>

              <span className="text-muted-foreground">Cluster</span>
              <span>{clusterName}</span>

              <span className="text-muted-foreground">Node</span>
              <span>{task.node || "—"}</span>

              <span className="text-muted-foreground">Type</span>
              <span className="font-mono">{task.task_type || "—"}</span>

              <span className="text-muted-foreground">Started</span>
              <span>{formatTime(task.started_at)}</span>

              <span className="text-muted-foreground">
                {task.finished_at ? "Finished" : "Elapsed"}
              </span>
              <span>
                {task.finished_at ? `${formatTime(task.finished_at)} ` : ""}
                <span className="text-muted-foreground">
                  ({formatDuration(task.started_at, task.finished_at)})
                </span>
              </span>

              {display === "failed" && exitText !== "" && (
                <>
                  <span className="text-muted-foreground">Exit Status</span>
                  <span className="text-red-500">{exitText}</span>
                </>
              )}

              <span className="text-muted-foreground">UPID</span>
              <span className="break-all font-mono text-[10px]">
                {task.upid}
              </span>
            </div>

            <div className="mt-2">
              <Button
                variant="outline"
                size="sm"
                onClick={(e) => {
                  e.stopPropagation();
                  setFocusedTask({
                    clusterId: task.cluster_id,
                    upid: task.upid,
                    description: task.description || task.task_type || "Task",
                  });
                }}
              >
                <Activity className="mr-1 h-3 w-3" />
                Live view
              </Button>
            </div>

            {/* Task log output */}
            <div className="mt-2 border-t pt-2">
              <span className="text-xs font-medium text-muted-foreground">
                Log
              </span>
              {logLoading && (
                <div className="mt-1 flex items-center gap-1 text-xs text-muted-foreground">
                  <Loader2 className="h-3 w-3 animate-spin" />
                  Loading log…
                </div>
              )}
              {logLines && logLines.length > 0 && (
                <pre className="mt-1 max-h-48 overflow-auto rounded bg-muted/50 p-2 font-mono text-[11px] leading-relaxed">
                  {logLines.map((line) => line.t).join("\n")}
                </pre>
              )}
              {logLines && logLines.length === 0 && (
                <div className="mt-1 text-xs text-muted-foreground">
                  No log output.
                </div>
              )}
            </div>
          </td>
        </tr>
      )}
    </>
  );
}

/** TasksPanel renders the full task-history table (filters + pagination +
 *  expandable rows). Hosted as the "Tasks" tab of the Events page. */
export function TasksPanel() {
  const [page, setPage] = useState(0);
  const [clusterFilter, setClusterFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const { data: clusters } = useClusters();
  const { data, isLoading, error } = useTasks({
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
    clusterId: clusterFilter || undefined,
    status: statusFilter || undefined,
  });

  const clusterName = (id: string): string => {
    const match = clusters?.find((c) => c.id === id);
    return match?.name ?? id.slice(0, 8);
  };

  const totalPages = data ? Math.ceil(data.total / PAGE_SIZE) : 0;

  return (
    <div className="space-y-4">
      {/* Filters */}
      <div className="flex flex-wrap gap-3">
        <select
          className={selectClass}
          value={clusterFilter}
          onChange={(e) => {
            setClusterFilter(e.target.value);
            setPage(0);
          }}
        >
          <option value="">All Clusters</option>
          {clusters?.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>

        <select
          className={selectClass}
          value={statusFilter}
          onChange={(e) => {
            setStatusFilter(e.target.value);
            setPage(0);
          }}
        >
          {statusFilters.map((s) => (
            <option key={s.value || "all"} value={s.value}>
              {s.label}
            </option>
          ))}
        </select>
      </div>

      {/* Table */}
      {isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : error ? (
        <p className="text-destructive">{error.message}</p>
      ) : (
        <>
          <div className="overflow-x-auto rounded-md border">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b bg-muted/50">
                  <th className="px-4 py-2 text-left font-medium">Started</th>
                  <th className="px-4 py-2 text-left font-medium">Cluster</th>
                  <th className="px-4 py-2 text-left font-medium">Type</th>
                  <th className="px-4 py-2 text-left font-medium">
                    Description
                  </th>
                  <th className="px-4 py-2 text-left font-medium">Node</th>
                  <th className="px-4 py-2 text-left font-medium">Status</th>
                </tr>
              </thead>
              <tbody>
                {data?.items.map((task) => (
                  <TaskRow
                    key={task.id}
                    task={task}
                    clusterName={clusterName(task.cluster_id)}
                    expanded={expandedId === task.id}
                    onToggle={() => {
                      setExpandedId(expandedId === task.id ? null : task.id);
                    }}
                  />
                ))}
                {data?.items.length === 0 && (
                  <tr>
                    <td
                      colSpan={6}
                      className="px-4 py-8 text-center text-muted-foreground"
                    >
                      No tasks found.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          {/* Pagination */}
          <div className="flex items-center justify-between">
            <p className="text-sm text-muted-foreground">
              {data ? `${String(data.total)} total tasks` : ""}
            </p>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={page === 0}
                onClick={() => {
                  setPage((p) => Math.max(0, p - 1));
                }}
              >
                <ChevronLeft className="h-4 w-4" />
              </Button>
              <span className="text-sm">
                Page {page + 1} of {Math.max(1, totalPages)}
              </span>
              <Button
                variant="outline"
                size="sm"
                disabled={page + 1 >= totalPages}
                onClick={() => {
                  setPage((p) => p + 1);
                }}
              >
                <ChevronRight className="h-4 w-4" />
              </Button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
