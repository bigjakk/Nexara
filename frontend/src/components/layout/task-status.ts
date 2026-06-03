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
 * Resolves an activity row's task status, preferring the most live source:
 *   1. live poll (present only for currently-running tasks we still poll)
 *   2. server-authoritative task_history status (Nexara-dispatched tasks)
 *   3. status carried in the audit details (ingested, already-finished
 *      external Proxmox tasks)
 *   4. none (non-task audit entries)
 */
export function deriveTaskStatus(
  entry: Pick<AuditLogEntry, "task_status" | "task_exit_status">,
  details: ParsedDetails,
  polled: { status: string; exitStatus: string } | undefined,
): DerivedTaskStatus {
  if (polled) {
    if (polled.status === "running") return "running";
    if (polled.status === "stopped")
      return isOkExit(polled.exitStatus) ? "ok" : "failed";
  }
  switch (entry.task_status) {
    case "running":
      return "running";
    case "completed":
      return "ok";
    case "failed":
      return "failed";
    case "stopped":
      // Some writers (migration orchestrator, DRS) persist the raw Proxmox
      // "stopped" state rather than completed/failed; classify by exit status.
      return isOkExit(entry.task_exit_status ?? "") ? "ok" : "failed";
    default:
      break;
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
