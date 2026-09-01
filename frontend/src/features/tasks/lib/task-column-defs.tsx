/**
 * How each task column renders.
 *
 * Split from task-columns.ts so that module stays JSX-free: these are the only
 * exports that need a renderer, and keeping the hooks and derivations out of a
 * .tsx keeps fast refresh working for both halves.
 */
import {
  CheckCircle2,
  ChevronDown,
  Loader2,
  Monitor,
  XCircle,
} from "lucide-react";
import type { DisplayStatus } from "@/components/layout/task-status";
import { TaskProgressCell } from "@/components/TaskProgressCell";
import type { ColumnDef } from "@/hooks/useColumnLayout";
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
const STATUS_LABEL: Record<DisplayStatus, string> = {
  running: "Running",
  ok: "Completed",
  failed: "Failed",
};

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString();
}

/**
 * Every column both task tables can render.
 *
 * Sorting is SERVER-side (see TaskSortKey), so these declare `sortable: true`
 * but no `sortValue`: the ordering is a query parameter, and re-sorting the
 * delivered page on top of the server's answer is exactly the bug server-side
 * sorting exists to avoid. They are declared here so the header row and every
 * body row are generated from one ordered list, which is what lets a dragged
 * column take its values with it.
 */
const ALL_COLUMNS: ColumnDef<TaskRecord, TaskSortKey, TaskCellCtx>[] = [
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
        {formatTime(task.started_at)}
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
        {task.source === "proxmox" && (
          <span className="inline-flex shrink-0 items-center gap-0.5 rounded-full bg-orange-500/10 px-1.5 py-0.5 text-[10px] leading-none font-medium text-orange-600 dark:text-orange-400">
            <Monitor className="h-2.5 w-2.5" />
            PVE
          </span>
        )}
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
 * The two column sets, as module-scope constants rather than a filter call.
 *
 * useColumnLayout needs a stable reference — a fresh array each render would
 * reset the layout on every keystroke — and the two tables persist their
 * layouts under different ids, so they are genuinely different tables rather
 * than one with a column hidden.
 */
const COLUMNS_WITHOUT_VM = ALL_COLUMNS.filter((c) => c.key !== "vm");

export const TASK_COLUMNS = COLUMNS_WITHOUT_VM;
export const TASK_COLUMNS_WITH_VM = ALL_COLUMNS;
