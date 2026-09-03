import { useState, type ReactNode } from "react";
import { ChevronLeft, ChevronRight, Loader2, Activity } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { DataTableHeadCells } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
import { useColumnLayout, type ColumnLayout } from "@/hooks/useColumnLayout";
import { formatDateTime } from "@/lib/format";
import { displayProgress } from "@/components/layout/task-status";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useTaskStatus, useTaskLog } from "@/features/vms/api/vm-queries";
import { useTaskLogStore } from "@/stores/task-log-store";
import { selectClass, statusFilters } from "../lib/task-filters";
import {
  deriveDisplayStatus,
  useTaskSort,
  type TaskCellCtx,
  type TaskSortKey,
} from "../lib/task-columns";
import { TASK_COLUMNS } from "../lib/task-column-defs";
import { useTasks, type TaskRecord } from "../api/tasks-queries";

const PAGE_SIZE = 50;

/**
 * The shared `<thead>`. Both task tables render the same columns from the same
 * layout, so adding or renaming one is a single edit in task-columns.tsx.
 */
export type TaskLayout = ColumnLayout<TaskRecord, TaskSortKey, TaskCellCtx>;

export function TaskTableHeader({
  layout,
  directionFor,
  onSort,
}: {
  layout: TaskLayout;
  directionFor: (key: TaskSortKey) => "asc" | "desc" | null;
  onSort: (key: TaskSortKey) => void;
}) {
  return (
    <thead>
      <tr className="border-b bg-muted/50">
        <DataTableHeadCells
          layout={layout}
          directionFor={directionFor}
          onSort={onSort}
        />
      </tr>
    </thead>
  );
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

export function TaskRow({
  task,
  clusterName,
  vmName,
  expanded,
  onToggle,
  layout,
}: {
  task: TaskRecord;
  clusterName: string;
  /** Rendered by the `vm` column, which only the folder-scoped layout
   * includes. */
  vmName?: ReactNode;
  expanded: boolean;
  onToggle: () => void;
  /** The parent's layout — the SAME instance its header row uses, so a
   * dragged column moves the heading and these cells together. */
  layout: TaskLayout;
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
  const progress = displayProgress(
    display,
    task.progress,
    display === "running" ? live?.progress : undefined,
  );
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
        <DataTableCells
          row={task}
          layout={layout}
          ctx={{
            clusterName,
            vmName,
            display,
            progress,
            expanded,
          }}
        />
      </tr>
      {expanded && (
        <tr className="border-b bg-muted/10">
          <td colSpan={layout.columns.length} className="px-4 py-3">
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
              <span>{formatDateTime(task.started_at)}</span>

              <span className="text-muted-foreground">
                {task.finished_at ? "Finished" : "Elapsed"}
              </span>
              <span>
                {task.finished_at ? `${formatDateTime(task.finished_at)} ` : ""}
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

  const { sort, toggle, directionFor } = useTaskSort(() => {
    setPage(0);
  });
  const layout = useColumnLayout("tasks", TASK_COLUMNS);

  const { data: clusters } = useClusters();
  const { data, isLoading, error } = useTasks({
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
    clusterId: clusterFilter || undefined,
    status: statusFilter || undefined,
    sort: sort.key,
    order: sort.direction,
  });

  const clusterName = (id: string): string => {
    const match = clusters?.find((c) => c.id === id);
    return match?.name ?? id.slice(0, 8);
  };

  const totalPages = data ? Math.ceil(data.total / PAGE_SIZE) : 0;

  return (
    <div className="space-y-4">
      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
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

        <span className="flex-1" />
        <ResetColumnsButton layout={layout} />
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
            <table
              className="table-fixed text-sm"
              style={{ width: layout.totalWidth }}
            >
              <TaskTableHeader
                layout={layout}
                directionFor={directionFor}
                onSort={toggle}
              />
              <tbody>
                {data?.items.map((task) => (
                  <TaskRow
                    key={task.id}
                    task={task}
                    clusterName={clusterName(task.cluster_id)}
                    layout={layout}
                    expanded={expandedId === task.id}
                    onToggle={() => {
                      setExpandedId(expandedId === task.id ? null : task.id);
                    }}
                  />
                ))}
                {data?.items.length === 0 && (
                  <tr>
                    <td
                      colSpan={layout.columns.length}
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
                aria-label="Previous page"
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
                aria-label="Next page"
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
