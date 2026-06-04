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

export function isOkExit(exitStatus: string): boolean {
  return (
    exitStatus === "" || exitStatus === "OK" || exitStatus.startsWith("WARNINGS")
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
