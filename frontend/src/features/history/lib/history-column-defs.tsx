/**
 * How each History column renders. Split from history.ts so that module
 * stays JSX-free, as task-column-defs.tsx is split from task-columns.ts.
 */
import { Link } from "react-router-dom";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  Circle,
  Loader2,
  XCircle,
} from "lucide-react";
import {
  STATUS_LABEL,
  type DisplayStatus,
} from "@/components/layout/task-status";
import { LinkedTaskBadge } from "@/components/LinkedTaskBadge";
import { PVESourceBadge } from "@/components/PVESourceBadge";
import { TaskProgressCell } from "@/components/TaskProgressCell";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { formatDateTime } from "@/lib/format";
import type { HistoryEntry } from "../types/history";
import type { HistoryCellCtx, HistoryColumnKey } from "./history";

/** A plain render helper rather than a component, so this module exports only
 *  data and fast refresh treats it as such. */
function statusIcon(status: DisplayStatus | null) {
  if (status === "running")
    return <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-500" />;
  if (status === "ok")
    return <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />;
  if (status === "failed")
    return <XCircle className="h-3.5 w-3.5 text-red-500" />;
  return <span className="inline-block h-3.5 w-3.5" />;
}

const STATUS_BADGE: Record<DisplayStatus, string> = {
  running: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  ok: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  failed: "bg-red-500/15 text-red-600 dark:text-red-400",
};

export const HISTORY_COLUMNS: ColumnDef<
  HistoryEntry,
  HistoryColumnKey,
  HistoryCellCtx
>[] = [
  {
    key: "time",
    label: "Time",
    width: 200,
    cell: (entry, ctx) => (
      <div className="flex items-center gap-1.5 whitespace-nowrap text-muted-foreground">
        <ChevronDown
          className={`h-3 w-3 shrink-0 text-muted-foreground transition-transform ${ctx.expanded ? "" : "-rotate-90"}`}
        />
        {statusIcon(ctx.display)}
        {formatDateTime(entry.occurred_at)}
      </div>
    ),
  },
  {
    key: "event",
    label: "Event",
    width: 210,
    cell: (entry, ctx) => {
      // A change no task explains is the one an operator is looking for.
      const Icon =
        entry.kind === "status" ? AlertTriangle : (ctx.icon ?? Circle);
      return (
        <div className="flex items-center gap-2">
          <Icon
            className={`h-3.5 w-3.5 shrink-0 ${entry.kind === "status" ? "text-amber-500" : "text-muted-foreground"}`}
          />
          {/* Beside a marker the name stays whole and the marker gives way. */}
          <span
            className={`font-medium ${ctx.linkedRole ? "shrink-0" : "truncate"}`}
          >
            {ctx.label}
          </span>
          <LinkedTaskBadge
            role={ctx.linkedRole}
            requester={ctx.requester}
            compact
          />
        </div>
      );
    },
  },
  {
    key: "summary",
    label: "Summary",
    width: 320,
    cell: (_entry, ctx) => (
      <span className="text-muted-foreground" title={ctx.summary}>
        {ctx.summary || "—"}
      </span>
    ),
  },
  {
    key: "who",
    label: "By",
    width: 170,
    hideBelowMd: true,
    cell: (entry, ctx) =>
      entry.kind === "status" ? (
        <span className="text-muted-foreground">—</span>
      ) : (
        <div className="flex items-center gap-1.5">
          <PVESourceBadge source={entry.source} />
          <span className="truncate">{ctx.actor || "—"}</span>
        </div>
      ),
  },
  {
    key: "node",
    label: "Node",
    width: 120,
    hideBelowMd: true,
    cell: (entry) => (
      <span className="text-muted-foreground">{entry.node || "—"}</span>
    ),
  },
  {
    key: "outcome",
    label: "Outcome",
    width: 130,
    cell: (_entry, ctx) => {
      if (ctx.display === null) {
        return <span className="text-muted-foreground">—</span>;
      }
      if (ctx.display === "running") {
        return <TaskProgressCell display="running" value={ctx.progress} />;
      }
      return (
        <span
          className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_BADGE[ctx.display]}`}
        >
          {STATUS_LABEL[ctx.display]}
        </span>
      );
    },
  },
];

/** The guest column of a History of many guests: a link where the page has
 *  one, else the VMID. */
const GUEST_COLUMN: ColumnDef<HistoryEntry, HistoryColumnKey, HistoryCellCtx> =
  {
    key: "guest",
    label: "Guest",
    width: 160,
    cell: (_entry, ctx) => {
      if (ctx.guest === null) {
        return <span className="text-muted-foreground">—</span>;
      }
      const { vmid, link } = ctx.guest;
      if (!link) {
        return <span className="text-muted-foreground">#{vmid}</span>;
      }
      return (
        <Link
          to={link.path}
          className="truncate hover:underline"
          onClick={(e) => {
            e.stopPropagation();
          }}
        >
          {link.name}
        </Link>
      );
    },
  };

export const HISTORY_COLUMNS_WITH_GUEST = HISTORY_COLUMNS.flatMap((column) =>
  column.key === "event" ? [column, GUEST_COLUMN] : [column],
);
