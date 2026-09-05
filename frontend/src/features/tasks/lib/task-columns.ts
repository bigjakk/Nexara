/**
 * Column definitions, sort state and the display derivations shared by the
 * Events-page TasksPanel and the folder-scoped FolderTasksTab.
 *
 * Both tables render the same rows through the same TaskRow, so the column
 * list, the sort keys and the rules for what a cell shows live here once
 * rather than being restated (and drifting) in each panel.
 */
import type { ReactNode } from "react";
import {
  deriveTaskStatus,
  isOkExit,
  type DisplayStatus,
} from "@/components/layout/task-status";
import { useSortState, type SortState } from "@/hooks/useTableSort";
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

/** Newest first — the order the table has always opened with. */
export const DEFAULT_TASK_SORT: SortState<TaskSortKey> = {
  key: "started",
  direction: "desc",
};

/**
 * Server-side sort state, shaped like useTableSort's return so the panels and
 * DataTableHead read the same either way. It holds no rows: the ordering
 * is a query parameter, and TanStack Query refetches when it changes.
 *
 * `onSortChange` resets pagination — a new ordering makes the current page
 * number meaningless, and page 5 of a freshly-sorted set is not where the
 * operator asked to be.
 */
export function useTaskSort(onSortChange: () => void) {
  const { sort, toggle, directionFor } = useSortState<TaskSortKey>(
    DEFAULT_TASK_SORT,
    onSortChange,
  );
  // Never null: it opens on DEFAULT_TASK_SORT and toggle only ever swaps one
  // sort for another. The panels hand sort.key straight to useTasks, which
  // requires both halves.
  return { sort: sort ?? DEFAULT_TASK_SORT, toggle, directionFor };
}

/**
 * Resolve the display status of a task_history row.
 *
 * The precedence is deriveTaskStatus's — server status authoritative once
 * terminal, live poll only to finish a row the server still calls running (the
 * d86b7df fix) — so this renames the row's fields into that signature rather
 * than restating the switch. What it adds is the "none" answer, which means
 * "not a task at all" and no task_history row ever is: absent a poll, a status
 * the switch does not name is classified by its exit status — the rule
 * sort_status applies in queries/tasks.sql.
 */
export function deriveDisplayStatus(
  task: TaskRecord,
  /** The poll for a row the server still reports as running — the only row
   * TaskRow polls, and the only one where a poll changes the answer. Pass one
   * for a row whose status the switch does not name and the poll, rather than
   * the exit status, decides it. */
  live: { status: string; exit_status: string } | undefined,
): DisplayStatus {
  const status = deriveTaskStatus(
    { task_status: task.status, task_exit_status: task.exit_status },
    {},
    live && { status: live.status, exitStatus: live.exit_status },
  );
  if (status !== "none") return status;
  return isOkExit(task.exit_status) ? "ok" : "failed";
}
