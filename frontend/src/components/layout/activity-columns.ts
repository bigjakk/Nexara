/**
 * The Activity panel's row decoration, severity rules and sort ranks.
 *
 * Separate from TaskLogPanel.tsx so the pure logic is unit-testable and fast
 * refresh keeps working — the same split as features/tasks/lib/task-columns.ts,
 * which does this job for the Tasks tables. How each column renders, and what
 * it sorts on, is next door in activity-column-defs.tsx.
 */
import type { AuditLogEntry } from "@/features/audit/api/audit-queries";
import type { SortState } from "@/hooks/useTableSort";
import { SYSTEM_USER_ID } from "@/lib/constants";
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

export function formatAction(action: string): string {
  return action.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

export type Severity = "info" | "warning" | "error";

/**
 * How loudly an audit entry should read.
 *
 * Takes the details already parsed rather than the raw JSON. The Activity
 * panel parses them anyway to derive the row, so the raw form had it parsing
 * the same string twice per row; the audit table parses at the call instead.
 * `parseDetails` yields `{}` for the empty / "{}" / "null" / unparseable
 * cases, so the absent-details guard the raw form needed is the empty object.
 */
export function deriveSeverity(
  action: string,
  details: ParsedDetails,
): Severity {
  if (typeof details["error"] === "string" && details["error"] !== "")
    return "error";
  if (details["status"] === "failed" || details["status"] === "error")
    return "error";
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
 * A Nexara account's name for display.
 *
 * The one thing it adds over `display_name || email` is the seeded system
 * actor. Migration 000013 gives that row the display name "DRS Scheduler",
 * but four subsystems write under it — DRS, the scheduler, the rolling-update
 * orchestrator and the collector — so on a rolling-update row the seeded name
 * is simply wrong. "System" is true for all four.
 *
 * Takes the three fields rather than an entry, so the audit page's user FILTER
 * can name its options by the same rule the rows are labelled with; it lists
 * `AuditUserRef`s, not audit entries. Empty when the row identifies no user at
 * all (a deleted account leaves the LEFT JOIN empty on both columns) — callers
 * render an em dash rather than inventing one.
 */
export function userLabel(
  id: string,
  displayName: string,
  email: string,
): string {
  if (id === SYSTEM_USER_ID) return "System";
  return displayName || email;
}

/**
 * Who to credit for an audit row — what the Activity drawer's User column and
 * the audit page's rows both show.
 *
 * A task Nexara ingested from Proxmox was performed by a *PVE* account, and
 * the collector records it in the details as `proxmox_user`. Every one of
 * those rows is written under the seeded system actor, so going by the users
 * join alone would credit a `root@pam` shutdown to Nexara's own background
 * user. Anything else is the Nexara account that wrote the entry.
 */
export function deriveActor(
  entry: Pick<AuditLogEntry, "user_id" | "user_display_name" | "user_email">,
  details: ParsedDetails,
): string {
  const pveUser = details["proxmox_user"];
  if (typeof pveUser === "string" && pveUser !== "") return pveUser;
  return userLabel(entry.user_id, entry.user_display_name, entry.user_email);
}

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
  /** Who did it — see deriveActor. Empty when nothing identifies an actor. */
  actorLabel: string;
  exitStatusText: string;
}

export function decorateActivity(
  entry: AuditLogEntry,
  taskStatuses: Record<string, LiveTaskStatus>,
): ActivityRowData {
  const details = parseDetails(entry.details);
  const live = details.upid ? taskStatuses[details.upid] : undefined;
  const status = deriveTaskStatus(entry, details, live);

  // A failed task outranks whatever the action name suggests.
  const severity: Severity =
    status === "failed" ? "error" : deriveSeverity(entry.action, details);

  // Non-task entries (a login, a token mint) have no progress to draw at all —
  // distinct from a task whose progress Proxmox never reported.
  const progress =
    status === "none"
      ? null
      : displayProgress(status, entry.task_progress ?? null, live?.progress);

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
    status,
    severity,
    progress,
    actionLabel: formatAction(entry.action),
    resourceLabel,
    actorLabel: deriveActor(entry, details),
    exitStatusText,
  };
}

/**
 * The Action cell as one string — what that column sorts on, and what the
 * task-progress dialog titles itself with. The cell renders the two halves
 * separately, so this is the only place the em dash is composed, and the only
 * place a row with no resource is kept from trailing one.
 */
export function activityLabel(row: ActivityRowData): string {
  return row.resourceLabel
    ? `${row.actionLabel} — ${row.resourceLabel}`
    : row.actionLabel;
}

export type ActivitySortKey =
  | "status"
  | "level"
  | "action"
  | "user"
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
 *
 * Read by the `sortValue`s in activity-column-defs.tsx; they live here, with
 * the types they rank, so the JSX file holds only what needs a renderer.
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

export const activityRowKey = (r: ActivityRowData) => r.entry.id;

/** Newest first — the order the panel has always opened with. */
export const DEFAULT_ACTIVITY_SORT: SortState<ActivitySortKey> = {
  key: "time",
  direction: "desc",
};
