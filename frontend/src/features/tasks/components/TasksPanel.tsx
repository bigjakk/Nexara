import { useEffect, useState, type ReactNode } from "react";
import { ChevronLeft, ChevronRight, Activity } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { DataTableHeadCells } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
import { TaskLogSection } from "@/components/TaskLogSection";
import { useColumnLayout, type ColumnLayout } from "@/hooks/useColumnLayout";
import type { SortDirection } from "@/hooks/useTableSort";
import { formatDateTime } from "@/lib/format";
import { displayProgress } from "@/components/layout/task-status";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useTaskStatus, useTaskLog } from "@/features/vms/api/vm-queries";
import { useTaskLogStore } from "@/stores/task-log-store";
import { PAGE_SIZE, selectClass, statusFilters } from "../lib/task-filters";
import {
  deriveDisplayStatus,
  useTaskSort,
  type TaskCellCtx,
  type TaskSortKey,
} from "../lib/task-columns";
import { TASK_COLUMNS } from "../lib/task-column-defs";
import { useTasks, type TaskRecord } from "../api/tasks-queries";

export type TaskLayout = ColumnLayout<TaskRecord, TaskSortKey, TaskCellCtx>;

/**
 * The `<thead>`. Rendered from the same layout the body cells use, so adding or
 * renaming a column is a single edit in task-column-defs.tsx.
 */
function TaskTableHeader({
  layout,
  directionFor,
  onSort,
}: {
  layout: TaskLayout;
  directionFor: (key: TaskSortKey) => SortDirection | null;
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

function TaskRow({
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

            <TaskLogSection lines={logLines} isLoading={logLoading} />
          </td>
        </tr>
      )}
    </>
  );
}

/** The status dropdown both task views carry. Resetting to page 0 is the
 *  caller's job — it owns the page state. */
export function TaskStatusFilter({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <select
      className={selectClass}
      value={value}
      onChange={(e) => {
        onChange(e.target.value);
      }}
    >
      {statusFilters.map((s) => (
        <option key={s.value || "all"} value={s.value}>
          {s.label}
        </option>
      ))}
    </select>
  );
}

/**
 * The task-history table itself: loading, error, rows, empty state and pager.
 *
 * Both task views render this one. What differs between them sits above it —
 * the Events page adds a cluster filter, the folder tab guards on how many VMs
 * the folder holds — so each owns its query and its filter chrome and hands
 * the page of results here. `expandedId` lives here because both views want
 * exactly one row open at a time and neither reads it.
 */
export function TaskHistoryTable({
  layout,
  directionFor,
  onSort,
  items,
  total,
  page,
  onPageChange,
  isLoading,
  error,
  clusterName,
  vmName,
  emptyMessage,
}: {
  layout: TaskLayout;
  directionFor: (key: TaskSortKey) => SortDirection | null;
  onSort: (key: TaskSortKey) => void;
  items: TaskRecord[];
  total: number;
  page: number;
  onPageChange: (page: number) => void;
  isLoading: boolean;
  error: Error | null;
  clusterName: (task: TaskRecord) => string;
  /** The `vm` cell's content, for the layout that includes that column. */
  vmName?: (task: TaskRecord) => ReactNode;
  emptyMessage: string;
}) {
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  // Snap back when a refetch (WS invalidation / 60s poll) shrinks the result
  // below the current page — otherwise a stale page index strands an empty
  // table. Safe against transient zeros: placeholderData in useTasks keeps the
  // previous `total` while the next page loads.
  //
  // Not while the fetch is failing, though — placeholder data is NOT applied in
  // the error state, so `total` really does collapse to 0 there. Clamping on
  // that would throw the operator back to page 1 and swap the error message for
  // page 1's cached rows before they could read it.
  useEffect(() => {
    if (!error && page > totalPages - 1) onPageChange(totalPages - 1);
  }, [error, page, totalPages, onPageChange]);

  if (isLoading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    );
  }
  if (error) {
    return <p className="text-destructive">{error.message}</p>;
  }

  return (
    <>
      <div className="overflow-x-auto rounded-md border">
        <table
          className="table-fixed text-sm"
          style={{ width: layout.totalWidth }}
        >
          <TaskTableHeader
            layout={layout}
            directionFor={directionFor}
            onSort={onSort}
          />
          <tbody>
            {items.map((task) => (
              <TaskRow
                key={task.id}
                task={task}
                clusterName={clusterName(task)}
                layout={layout}
                vmName={vmName?.(task)}
                expanded={expandedId === task.id}
                onToggle={() => {
                  setExpandedId(expandedId === task.id ? null : task.id);
                }}
              />
            ))}
            {items.length === 0 && (
              <tr>
                <td
                  colSpan={layout.columns.length}
                  className="px-4 py-8 text-center text-muted-foreground"
                >
                  {emptyMessage}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          {String(total)} task{total === 1 ? "" : "s"}
        </p>
        {totalPages > 1 && (
          <div className="flex items-center gap-2">
            <Button
              aria-label="Previous page"
              variant="outline"
              size="sm"
              disabled={page === 0}
              onClick={() => {
                onPageChange(Math.max(0, page - 1));
              }}
            >
              <ChevronLeft className="h-4 w-4" />
            </Button>
            <span className="text-sm">
              Page {page + 1} of {totalPages}
            </span>
            <Button
              aria-label="Next page"
              variant="outline"
              size="sm"
              disabled={page + 1 >= totalPages}
              onClick={() => {
                onPageChange(page + 1);
              }}
            >
              <ChevronRight className="h-4 w-4" />
            </Button>
          </div>
        )}
      </div>
    </>
  );
}

/** TasksPanel renders the full task-history table (filters + pagination +
 *  expandable rows). Hosted as the "Tasks" tab of the Events page. */
export function TasksPanel() {
  const [page, setPage] = useState(0);
  const [clusterFilter, setClusterFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");

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

        <TaskStatusFilter
          value={statusFilter}
          onChange={(v) => {
            setStatusFilter(v);
            setPage(0);
          }}
        />

        <span className="flex-1" />
        <ResetColumnsButton layout={layout} />
      </div>

      <TaskHistoryTable
        layout={layout}
        directionFor={directionFor}
        onSort={toggle}
        items={data?.items ?? []}
        total={data?.total ?? 0}
        page={page}
        onPageChange={setPage}
        isLoading={isLoading}
        error={error}
        clusterName={(task) => clusterName(task.cluster_id)}
        emptyMessage="No tasks found."
      />
    </div>
  );
}
