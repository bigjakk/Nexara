/**
 * The Activity panel's column definitions, sort accessors, and the per-row
 * derivations the cells render.
 *
 * Separate from TaskLogPanel.tsx so the pure logic is unit-testable and fast
 * refresh keeps working — the same split as features/tasks/lib/task-columns.ts,
 * which does this job for the Tasks tables.
 */
import type { AuditLogEntry } from "@/features/audit/api/audit-queries";
import type { SortAccessors, SortState } from "@/hooks/useTableSort";
import {
  deriveTaskStatus,
  displayProgress,
  parseDetails,
  type DerivedTaskStatus,
  type ParsedDetails,
} from "./task-status";

/** The live poll's view of one task, keyed by UPID in TaskLogPanel state. */
export interface LiveTaskStatus {
  status: string;
  exitStatus: string;
  progress?: number;
}

export function formatRelativeTime(iso: string): string {
  const ago = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (ago < 60) return `${String(ago)}s ago`;
  if (ago < 3600) return `${String(Math.floor(ago / 60))}m ago`;
  if (ago < 86400) return `${String(Math.floor(ago / 3600))}h ago`;
  return `${String(Math.floor(ago / 86400))}d ago`;
}

export function formatTimestamp(iso: string): string {
  return new Date(iso).toLocaleString();
}

export function formatAction(action: string): string {
  return action.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

export type Severity = "info" | "warning" | "error";

export function deriveSeverity(action: string, details: string): Severity {
  if (details && details !== "{}" && details !== "null") {
    try {
      const d = JSON.parse(details) as Record<string, unknown>;
      if (typeof d["error"] === "string" && d["error"] !== "") return "error";
      if (d["status"] === "failed" || d["status"] === "error") return "error";
    } catch {
      // ignore
    }
  }
  const a = action.toLowerCase();
  if (a.includes("error") || a.includes("failed") || a.includes("fail"))
    return "error";
  if (
    a.includes("delete") ||
    a.includes("destroy") ||
    a.includes("disable") ||
    a.includes("revoke") ||
    a.includes("reset") ||
    a.includes("stop") ||
    a.includes("shutdown") ||
    a.includes("suspend") ||
    a.includes("cancel")
  )
    return "warning";
  return "info";
}

export const SEVERITY_STYLES: Record<Severity, string> = {
  info: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  warning: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  error: "bg-red-500/15 text-red-600 dark:text-red-400",
};

export const SEVERITY_LABELS: Record<Severity, string> = {
  info: "info",
  warning: "warn",
  error: "error",
};

/**
 * One activity row with everything the cells render already resolved.
 *
 * Computed once per entry rather than inside the row component, because the
 * sort accessors below must order on the SAME values that reach the screen —
 * severity is overridden by task failure, the action cell appends the resource,
 * and progress is derived. Sorting the raw fields would produce an order that
 * contradicts the table.
 */
export interface ActivityRowData {
  entry: AuditLogEntry;
  details: ParsedDetails;
  upid: string | undefined;
  status: DerivedTaskStatus;
  severity: Severity;
  /**
   * The fraction the Progress cell draws, or null when there is none —
   * which covers BOTH a non-task entry and a task whose progress Proxmox
   * never reported. `status` is what tells those two apart, so the cell needs
   * both values: null + "running" is the indeterminate bar, null + anything
   * else is an em dash.
   */
  progress: number | null;
  actionLabel: string;
  resourceLabel: string;
  exitStatusText: string;
}

export function decorateActivity(
  entry: AuditLogEntry,
  taskStatuses: Record<string, LiveTaskStatus>,
): ActivityRowData {
  const details = parseDetails(entry.details);
  const upid = details.upid;
  const live = upid ? taskStatuses[upid] : undefined;
  const status = deriveTaskStatus(entry, details, live);

  // A failed task outranks whatever the action name suggests.
  const severity: Severity =
    status === "failed" ? "error" : deriveSeverity(entry.action, entry.details);

  // Non-task entries (a login, a token mint) have no progress to draw at all —
  // distinct from a task whose progress Proxmox never reported.
  const progress =
    status === "none"
      ? null
      : displayProgress(
          status,
          entry.task_progress ?? null,
          status === "running" ? live?.progress : undefined,
        );

  // For Proxmox-sourced entries, resolve resource_name from the details JSON.
  let resourceLabel =
    entry.resource_name && entry.resource_vmid
      ? `${entry.resource_name} (${String(entry.resource_vmid)})`
      : entry.resource_name || entry.resource_id;
  if (!entry.resource_name && details["resource_name"]) {
    const rn =
      typeof details["resource_name"] === "string"
        ? details["resource_name"]
        : null;
    const ri =
      typeof details["resource_id"] === "string"
        ? details["resource_id"]
        : null;
    if (rn) {
      resourceLabel = ri ? `${rn} (${ri})` : rn;
    }
  }

  const exitStatusText =
    live?.exitStatus ||
    entry.task_exit_status ||
    (typeof details["status"] === "string" ? details["status"] : "");

  return {
    entry,
    details,
    upid,
    status,
    severity,
    progress,
    actionLabel: formatAction(entry.action),
    resourceLabel,
    exitStatusText,
  };
}

export type ActivitySortKey =
  | "status"
  | "level"
  | "action"
  | "cluster"
  | "progress"
  | "time";

/**
 * Rank by what the badge MEANS, not what it reads. Alphabetically the first
 * click on Level would put "error" first only by luck and "info" before
 * "warn" always; ranking makes an ascending click mean "worst first" on both
 * of these columns.
 *
 * "none" ranks as null — a login is not a task with an unknown outcome, it has
 * no outcome, and useTableSort floats nulls to the bottom in both directions
 * so a screenful of them never buries the tasks.
 */
export const STATUS_RANK: Record<DerivedTaskStatus, number | null> = {
  failed: 0,
  ok: 1,
  running: 2,
  none: null,
};
export const SEVERITY_RANK: Record<Severity, number> = {
  error: 0,
  warning: 1,
  info: 2,
};

export const ACTIVITY_ACCESSORS: SortAccessors<
  ActivityRowData,
  ActivitySortKey
> = {
  status: (r) => STATUS_RANK[r.status],
  level: (r) => SEVERITY_RANK[r.severity],
  // The whole visible string: the cell renders the action and the resource
  // together, so ordering on the action alone would look arbitrary within a
  // run of identically-named actions.
  action: (r) =>
    r.resourceLabel ? `${r.actionLabel} — ${r.resourceLabel}` : r.actionLabel,
  cluster: (r) => r.entry.cluster_name || null,
  progress: (r) => r.progress,
  time: (r) => new Date(r.entry.created_at).getTime(),
};

export const activityRowKey = (r: ActivityRowData) => r.entry.id;

/** Newest first — the order the panel has always opened with. */
export const DEFAULT_ACTIVITY_SORT: SortState<ActivitySortKey> = {
  key: "time",
  direction: "desc",
};
