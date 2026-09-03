/**
 * Column definitions, sort state and the display derivations shared by the
 * Events-page TasksPanel and the folder-scoped FolderTasksTab.
 *
 * Both tables render the same rows through the same TaskRow, so the column
 * list, the sort keys and the rules for what a cell shows live here once
 * rather than being restated (and drifting) in each panel.
 */
import { useState, type ReactNode } from "react";
import { isOkExit, type DisplayStatus } from "@/components/layout/task-status";
import type { SortDirection } from "@/hooks/useTableSort";
import type { TaskRecord } from "../api/tasks-queries";

/**
 * What a task cell needs beyond the row: the resolved cluster name, the VM
 * link the folder view supplies, and the display status and progress TaskRow
 * derives from its own live poll.
 *
 * Declared here, away from the cells that consume it, so this module stays
 * free of JSX — the hooks and derivations below are unit-tested with no
 * renderer involved.
 */
export interface TaskCellCtx {
  clusterName: string;
  vmName: ReactNode;
  display: DisplayStatus;
  progress: number | null;
  expanded: boolean;
}

/**
 * The sortable columns, by the name the API expects in ?sort=.
 *
 * Sorting is server-side: the table pages 50 rows out of a history that runs
 * to thousands, so ordering has to be applied to the whole filtered set —
 * reordering the delivered page would only ever reshuffle what is already on
 * screen. Keep in lockstep with taskSortColumns in
 * internal/api/handlers/tasks.go, which rejects anything not on its whitelist.
 */
export type TaskSortKey =
  | "started"
  | "cluster"
  | "type"
  | "description"
  | "vm"
  | "node"
  | "progress"
  | "status";

export interface TaskSortState {
  key: TaskSortKey;
  direction: SortDirection;
}

/** Newest first — the order the table has always opened with. */
export const DEFAULT_TASK_SORT: TaskSortState = {
  key: "started",
  direction: "desc",
};

/**
 * Server-side sort state, shaped like useTableSort's return so the panels and
 * DataTableHead read the same either way. It holds no rows: the ordering
 * is a query parameter, and TanStack Query refetches when it changes.
 *
 * `onSortChange` exists to reset pagination — a new ordering makes the current
 * page number meaningless, and page 5 of a freshly-sorted set is not where the
 * operator asked to be.
 */
export function useTaskSort(onSortChange?: () => void) {
  const [sort, setSort] = useState<TaskSortState>(DEFAULT_TASK_SORT);

  function toggle(key: TaskSortKey) {
    setSort((prev) =>
      prev.key === key
        ? { key, direction: prev.direction === "asc" ? "desc" : "asc" }
        : { key, direction: "asc" },
    );
    onSortChange?.();
  }

  /** The active direction for `key`, or null when another column is sorted. */
  function directionFor(key: TaskSortKey): SortDirection | null {
    return sort.key === key ? sort.direction : null;
  }

  return { sort, toggle, directionFor };
}

/**
 * Resolve the display status. The reconciled task_history status is
 * authoritative once terminal (preserves the d86b7df fix); only a row the
 * server still reports as running is refined by the live poll, so it flips to
 * done before the next reconcile tick.
 */
export function deriveDisplayStatus(
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
