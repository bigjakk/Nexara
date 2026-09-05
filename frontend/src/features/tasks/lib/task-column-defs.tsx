/**
 * How each task column renders.
 *
 * Split from task-columns.ts so that module stays JSX-free: these are the only
 * exports that need a renderer, and keeping the hooks and derivations out of a
 * .tsx keeps fast refresh working for both halves.
 */
import { CheckCircle2, ChevronDown, Loader2, XCircle } from "lucide-react";
import {
  STATUS_LABEL,
  type DisplayStatus,
} from "@/components/layout/task-status";
import { PVESourceBadge } from "@/components/PVESourceBadge";
import { TaskProgressCell } from "@/components/TaskProgressCell";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { formatDateTime } from "@/lib/format";
import type { TaskRecord } from "../api/tasks-queries";
import type { TaskCellCtx, TaskSortKey } from "./task-columns";

/** A plain render helper rather than a component, so this module exports only
 *  data and fast refresh treats it as such. */
function statusIcon(status: DisplayStatus) {
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

/**
 * Every column both task tables can render — the guest-scoped set, VM included.
 *
 * Sorting is SERVER-side (see TaskSortKey), so these declare `sortable: true`
 * but no `sortValue`: the ordering is a query parameter, and re-sorting the
 * delivered page on top of the server's answer is exactly the bug server-side
 * sorting exists to avoid. They are declared here so the header row and every
 * body row are generated from one ordered list, which is what lets a dragged
 * column take its values with it.
 */
export const TASK_COLUMNS_WITH_VM: ColumnDef<
  TaskRecord,
  TaskSortKey,
  TaskCellCtx
>[] = [
  {
    key: "started",
    label: "Started",
    width: 210,
    sortable: true,
    cell: (task, ctx) => (
      <div className="flex items-center gap-1.5 whitespace-nowrap text-muted-foreground">
        <ChevronDown
          className={`h-3 w-3 shrink-0 text-muted-foreground transition-transform ${ctx.expanded ? "" : "-rotate-90"}`}
        />
        {statusIcon(ctx.display)}
        {formatDateTime(task.started_at)}
      </div>
    ),
  },
  {
    key: "cluster",
    label: "Cluster",
    width: 130,
    sortable: true,
    cell: (_task, ctx) => ctx.clusterName,
  },
  {
    key: "type",
    label: "Type",
    width: 140,
    sortable: true,
    cell: (task) => (
      <span className="font-mono text-xs">{task.task_type || "—"}</span>
    ),
  },
  {
    key: "description",
    label: "Description",
    width: 300,
    sortable: true,
    cell: (task) => (
      <div className="flex items-center gap-2">
        <PVESourceBadge source={task.source} />
        <span className="truncate">{task.description || task.upid}</span>
      </div>
    ),
  },
  {
    key: "vm",
    label: "VM",
    width: 170,
    sortable: true,
    cell: (_task, ctx) => ctx.vmName,
  },
  {
    key: "node",
    label: "Node",
    width: 130,
    sortable: true,
    cell: (task) => (
      <span className="text-muted-foreground">{task.node || "—"}</span>
    ),
  },
  {
    key: "progress",
    label: "Progress",
    width: 130,
    sortable: true,
    cell: (_task, ctx) => (
      <TaskProgressCell display={ctx.display} value={ctx.progress} />
    ),
  },
  {
    key: "status",
    label: "Status",
    width: 120,
    sortable: true,
    cell: (_task, ctx) => (
      <span
        className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_BADGE[ctx.display]}`}
      >
        {STATUS_LABEL[ctx.display]}
      </span>
    ),
  },
];

/**
 * The Events-page set: the same columns without VM, which only a guest-scoped
 * view can fill.
 *
 * Filtered once at module scope rather than per render. Not because
 * useColumnLayout would break — it reconciles on the joined column KEYS, not on
 * the array's identity, so an equal-but-fresh array costs only a few recomputed
 * memos. It is useDataTable that needs a stable reference, for the accessors it
 * memoises on `[columns]`; these two tables sort server-side and use
 * useColumnLayout directly, so they never hit that. Module scope is simply
 * where a constant belongs.
 *
 * The two tables persist their layouts under different ids, so they are
 * genuinely different tables rather than one with a column hidden.
 */
export const TASK_COLUMNS = TASK_COLUMNS_WITH_VM.filter((c) => c.key !== "vm");
