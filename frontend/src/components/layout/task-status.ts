import type { AuditLogEntry } from "@/features/audit/api/audit-queries";

export interface ParsedDetails {
  upid?: string;
  node?: string;
  vmid?: number;
  [key: string]: unknown;
}

export function parseDetails(detailsStr: string): ParsedDetails {
  try {
    const parsed: unknown = JSON.parse(detailsStr);
    if (parsed && typeof parsed === "object") {
      return parsed as ParsedDetails;
    }
  } catch {
    // ignore
  }
  return {};
}

export type DerivedTaskStatus = "running" | "ok" | "failed" | "none";

// isOkExit is the frontend mirror of Go's proxmox.TaskSucceeded — keep the two
// in lockstep. Proxmox emits "OK", "OK (with warnings)" and "WARNINGS: N" for
// successful tasks; everything else is a failure.
export function isOkExit(exitStatus: string): boolean {
  const s = exitStatus.trim().toUpperCase();
  return (
    s === "" || s === "OK" || s.startsWith("OK ") || s.startsWith("WARNINGS")
  );
}

/**
 * Resolves an activity row's task status. The server-authoritative
 * task_history status (entry.task_status) is the source of truth and wins
 * outright once it is terminal — a stale "running" left in a poller's cache
 * must never override a completed/failed server status (that was the
 * "stuck on running after the task finished" bug).
 *
 * The live poll is consulted only to make a *still-running* task flip to done
 * sooner than the next reconcile tick, and as a fallback for ingested external
 * Proxmox tasks that have no task_history row. Precedence:
 *   1. server task_history status, when terminal (completed / failed / stopped)
 *   2. live poll, while the server still reports running (flip-to-done early)
 *   3. live poll / status carried in the audit details, for entries with no
 *      task_history status (ingested external tasks)
 *   4. none (non-task audit entries)
 */
export function deriveTaskStatus(
  entry: Pick<AuditLogEntry, "task_status" | "task_exit_status">,
  details: ParsedDetails,
  polled: { status: string; exitStatus: string } | undefined,
): DerivedTaskStatus {
  switch (entry.task_status) {
    case "completed":
      return "ok";
    case "failed":
      return "failed";
    case "stopped":
      // Some writers (migration orchestrator, DRS) persist the raw Proxmox
      // "stopped" state rather than completed/failed; classify by exit status.
      return isOkExit(entry.task_exit_status ?? "") ? "ok" : "failed";
    case "running":
      // Server still reports running; a live poll may know it finished sooner.
      if (polled) {
        if (polled.status === "running") return "running";
        if (polled.status === "stopped")
          return isOkExit(polled.exitStatus) ? "ok" : "failed";
      }
      return "running";
    default:
      break;
  }
  // No task_history status (ingested external Proxmox task): use the live poll,
  // then the finished status carried in the audit details.
  if (polled) {
    if (polled.status === "running") return "running";
    if (polled.status === "stopped")
      return isOkExit(polled.exitStatus) ? "ok" : "failed";
  }
  if (
    details.upid &&
    typeof details["status"] === "string" &&
    details["status"] !== ""
  ) {
    return isOkExit(details["status"]) ? "ok" : "failed";
  }
  return "none";
}

/** A task status that has something to draw — `deriveTaskStatus` minus the
 *  "not a task at all" case. */
export type DisplayStatus = Exclude<DerivedTaskStatus, "none">;

/**
 * The fraction a progress cell draws, or null when there is none to draw.
 *
 * A finished task shows a full bar whatever it stored: Proxmox reports no
 * progress for most task types, so "completed" and "completed at 0%" are the
 * same row and only one of them is true. A failed task keeps the fraction it
 * reached, which is the useful part of a failed migration. Null means Proxmox
 * never reported one — the cell says so rather than drawing 0%, which would
 * read as "made no progress" instead of "we don't know".
 *
 * Mirrored by sort_progress in queries/tasks.sql, which orders the whole task
 * history on the same rule; the SQL cannot see `live`, so a running row's
 * ordering can trail its readout by up to one reconcile tick.
 */
export function displayProgress(
  display: DisplayStatus,
  stored: number | null,
  live: number | undefined,
): number | null {
  if (display === "ok") return 1;
  if (display === "running") return live ?? stored;
  return stored;
}
