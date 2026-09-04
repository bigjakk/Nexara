/**
 * How each Activity column renders, and what it sorts on.
 *
 * Split from activity-columns.ts so that module stays JSX-free: this is the
 * only export needing a renderer, and keeping the decorator out of a .tsx keeps
 * fast refresh working for both halves. The `sortValue`s live here rather than
 * in a parallel accessor map so a column's heading, width, cell and sort key
 * are one declaration — useDataTable derives the accessors from them.
 */
import {
  Activity,
  CheckCircle2,
  ChevronRight,
  Loader2,
  XCircle,
} from "lucide-react";
import { PVESourceBadge } from "@/components/PVESourceBadge";
import { TaskProgressCell } from "@/components/TaskProgressCell";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { formatRelativeTime } from "@/lib/format";
import {
  activityLabel,
  SEVERITY_LABELS,
  SEVERITY_RANK,
  SEVERITY_STYLES,
  STATUS_RANK,
  type ActivityRowData,
  type ActivitySortKey,
} from "./activity-columns";

/** Whether this row is expanded, for the leading chevron. */
export interface ActivityCellCtx {
  expanded: boolean;
}

/*
 * Below `md` there is not enough width for six columns, so Cluster and
 * Progress yield — via `hideBelowMd`, which drops them from the layout rather
 * than hiding them with a class. A hidden-by-CSS column still contributes its
 * width, so the table would stay as wide as if it were shown and the drawer
 * would scroll sideways on a phone anyway.
 */
const ALL_COLUMNS: ColumnDef<
  ActivityRowData,
  ActivitySortKey,
  ActivityCellCtx
>[] = [
  {
    key: "status",
    label: "Status",
    width: 84,
    sortValue: (row) => STATUS_RANK[row.status],
    cell: (row, ctx) => (
      <div className="flex items-center gap-1.5">
        <ChevronRight
          className={`h-3 w-3 shrink-0 text-muted-foreground transition-transform ${ctx.expanded ? "rotate-90" : ""}`}
        />
        <span
          className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-md ${
            row.status === "running"
              ? "bg-blue-500/10"
              : row.status === "ok"
                ? "bg-emerald-500/10"
                : row.status === "failed"
                  ? "bg-red-500/10"
                  : "bg-muted"
          }`}
        >
          {row.status === "running" && (
            <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-500" />
          )}
          {row.status === "ok" && (
            <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />
          )}
          {row.status === "failed" && (
            <XCircle className="h-3.5 w-3.5 text-red-500" />
          )}
          {row.status === "none" && (
            <Activity className="h-3.5 w-3.5 text-muted-foreground" />
          )}
        </span>
      </div>
    ),
  },
  {
    key: "level",
    label: "Level",
    width: 70,
    sortValue: (row) => SEVERITY_RANK[row.severity],
    cell: (row) => (
      <span
        className={`inline-block rounded-full px-1.5 py-0.5 text-[10px] leading-none font-medium ${SEVERITY_STYLES[row.severity]}`}
      >
        {SEVERITY_LABELS[row.severity]}
      </span>
    ),
  },
  {
    key: "action",
    label: "Action",
    width: 460,
    // Sorted on the whole visible string: the cell renders the action and the
    // resource together, so ordering on the action alone would look arbitrary
    // within a run of identically-named actions.
    sortValue: activityLabel,
    cell: (row) => (
      <div className="flex items-center gap-x-2 overflow-hidden">
        <PVESourceBadge source={row.entry.source} />
        <span className="shrink-0 font-medium">{row.actionLabel}</span>
        {row.resourceLabel && (
          <span className="truncate text-muted-foreground">
            — {row.resourceLabel}
          </span>
        )}
        {/* The Progress column is hidden below md, so a running task's bar
              rides along here — where it lived for every width before Progress
              became a column of its own. */}
        {row.status === "running" && (
          <span className="shrink-0 md:hidden">
            <TaskProgressCell display={row.status} value={row.progress} />
          </span>
        )}
      </div>
    ),
  },
  {
    key: "cluster",
    label: "Cluster",
    width: 160,
    sortValue: (row) => row.entry.cluster_name || null,
    hideBelowMd: true,
    cell: (row) => (
      <span className="text-muted-foreground">
        {row.entry.cluster_name || "—"}
      </span>
    ),
  },
  {
    key: "progress",
    label: "Progress",
    width: 120,
    sortValue: (row) => row.progress,
    hideBelowMd: true,
    cell: (row) => (
      <TaskProgressCell
        display={row.status === "none" ? null : row.status}
        value={row.progress}
      />
    ),
  },
  {
    key: "time",
    label: "Time",
    width: 104,
    sortValue: (row) => new Date(row.entry.created_at).getTime(),
    align: "right",
    cell: (row) => (
      <span className="font-mono text-[11px] tabular-nums text-muted-foreground">
        {formatRelativeTime(row.entry.created_at)}
      </span>
    ),
  },
];

export const ACTIVITY_COLUMN_DEFS = ALL_COLUMNS;
