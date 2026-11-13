/**
 * How each Activity column renders, and what it sorts on.
 *
 * Split from activity-columns.ts so that module stays JSX-free: this is the
 * only export needing a renderer, and keeping the decorator out of a .tsx keeps
 * fast refresh working for both halves. The `sortValue`s live here rather than
 * in a parallel accessor map so a column's heading, width, cell and sort key
 * are one declaration — useDataTable derives the accessors from them.
 */
import { ChevronRight } from "lucide-react";
import { LinkedTaskBadge } from "@/components/LinkedTaskBadge";
import { PVESourceBadge } from "@/components/PVESourceBadge";
import { TaskProgressCell } from "@/components/TaskProgressCell";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { auditClusterLabel } from "@/features/audit/lib/audit-cluster";
import { formatRelativeTime } from "@/lib/format";
import {
  activityLabel,
  SEVERITY_LABELS,
  SEVERITY_RANK,
  STATUS_RANK,
  type ActivityRowData,
  type ActivitySortKey,
  type Severity,
} from "./activity-columns";
import { STATUS_LABEL, type DerivedTaskStatus } from "./task-status";

/**
 * A row's state as a dot: a running task blue, a failed one red, a finished
 * one quiet. An entry with no task behind it has no state to
 * show, so it gets a hollow ring: the column still lines up, and says so.
 */
const STATUS_DOT: Record<DerivedTaskStatus, string> = {
  running: "animate-pulse bg-info",
  ok: "bg-muted-foreground/45",
  failed: "bg-red-500",
  none: "ring-[1.5px] ring-inset ring-muted-foreground/40",
};

/** How loudly a level reads: a dot and a word, and colour only for trouble. */
const LEVEL_DOT: Record<Severity, string> = {
  info: "bg-muted-foreground/45",
  warning: "bg-amber-500",
  error: "bg-red-500",
};
const LEVEL_TEXT: Record<Severity, string> = {
  info: "text-muted-foreground",
  warning: "text-amber-600 dark:text-amber-400",
  error: "text-red-600 dark:text-red-400",
};

/** Whether this row is expanded, for the leading chevron. */
export interface ActivityCellCtx {
  expanded: boolean;
}

/*
 * Below `md` there is not enough width for seven columns, so User, Cluster and
 * Progress yield — via `hideBelowMd`, which drops them from the layout rather
 * than hiding them with a class. A hidden-by-CSS column still contributes its
 * width, so the table would stay as wide as if it were shown and the drawer
 * would scroll sideways on a phone anyway. All three are still in the expanded
 * row, which is the phone-width way to read them.
 */
export const ACTIVITY_COLUMN_DEFS: ColumnDef<
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
      <div className="flex items-center gap-2.5">
        <ChevronRight
          className={`h-3 w-3 shrink-0 text-muted-foreground transition-transform ${ctx.expanded ? "rotate-90" : ""}`}
        />
        <span
          role="img"
          aria-label={
            row.status === "none" ? "Not a task" : STATUS_LABEL[row.status]
          }
          className={`h-2 w-2 shrink-0 rounded-full ${STATUS_DOT[row.status]}`}
        />
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
        className={`inline-flex items-center gap-1.5 ${LEVEL_TEXT[row.severity]}`}
      >
        <span
          aria-hidden="true"
          className={`h-1.5 w-1.5 shrink-0 rounded-full ${LEVEL_DOT[row.severity]}`}
        />
        {SEVERITY_LABELS[row.severity]}
      </span>
    ),
  },
  {
    key: "action",
    label: "Action",
    width: 400,
    // Sorted on the whole visible string: the cell renders the action and the
    // resource together, so ordering on the action alone would look arbitrary
    // within a run of identically-named actions.
    sortValue: activityLabel,
    cell: (row) => (
      <div className="flex items-center gap-x-2 overflow-hidden">
        <PVESourceBadge source={row.entry.source} />
        <LinkedTaskBadge
          role={row.linkedRole}
          requester={row.haRequester}
          compact
        />
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
    key: "user",
    label: "User",
    width: 150,
    // Sorted on the resolved actor, not entry.user_display_name: a PVE row
    // shows the Proxmox account and a background row shows "System", and
    // ordering on the join column would scatter both through the "D"s.
    sortValue: (row) => row.actorLabel || null,
    hideBelowMd: true,
    // The cell's own <td> already truncates (DataTableCells), the same as the
    // Cluster column next door — no wrapper of its own needed.
    cell: (row) => (
      <span className="text-muted-foreground">{row.actorLabel || "—"}</span>
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
        {auditClusterLabel(row.entry, "—")}
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
        completedLabel
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
