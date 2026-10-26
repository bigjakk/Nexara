/**
 * How each History column renders. Split from guest-history.ts so that module
 * stays JSX-free, as task-column-defs.tsx is split from task-columns.ts.
 */
import {
  AlertTriangle,
  Archive,
  ArrowLeftRight,
  Camera,
  CheckCircle2,
  ChevronDown,
  Circle,
  Copy,
  Loader2,
  Package,
  Power,
  Settings2,
  ShieldCheck,
  Terminal,
  XCircle,
  type LucideIcon,
} from "lucide-react";
import {
  STATUS_LABEL,
  type DisplayStatus,
} from "@/components/layout/task-status";
import { PVESourceBadge } from "@/components/PVESourceBadge";
import { TaskProgressCell } from "@/components/TaskProgressCell";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { formatDateTime } from "@/lib/format";
import type {
  GuestHistoryCategory,
  GuestHistoryEntry,
} from "../api/guest-history-queries";
import type {
  GuestHistoryCellCtx,
  GuestHistoryColumnKey,
} from "./guest-history";

const CATEGORY_ICON: Record<GuestHistoryCategory, LucideIcon> = {
  power: Power,
  migration: ArrowLeftRight,
  snapshot: Camera,
  backup: Archive,
  clone: Copy,
  lifecycle: Package,
  config: Settings2,
  ha: ShieldCheck,
  console: Terminal,
  other: Circle,
};

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

export const GUEST_HISTORY_COLUMNS: ColumnDef<
  GuestHistoryEntry,
  GuestHistoryColumnKey,
  GuestHistoryCellCtx
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
        entry.kind === "status" ? AlertTriangle : CATEGORY_ICON[entry.category];
      return (
        <div className="flex items-center gap-2">
          <Icon
            className={`h-3.5 w-3.5 shrink-0 ${entry.kind === "status" ? "text-amber-500" : "text-muted-foreground"}`}
          />
          <span className="truncate font-medium">{ctx.label}</span>
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
