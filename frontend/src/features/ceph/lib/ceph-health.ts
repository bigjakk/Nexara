// Shared helpers for mapping Ceph health status strings to UI severity, labels,
// colors, and for aggregating health across clusters. Keeping this in one place
// keeps the badge, dashboard card, sidebar dot, and global indicator consistent.

import type { HealthSeverity } from "@/types/api";

export type CephSeverity = "ok" | "warn" | "err" | "unknown";

/** Maps a Ceph status string (HEALTH_OK/WARN/ERR) to a severity bucket. */
export function cephSeverity(status: string | undefined | null): CephSeverity {
  switch (status) {
    case "HEALTH_OK":
      return "ok";
    case "HEALTH_WARN":
      return "warn";
    case "HEALTH_ERR":
      return "err";
    default:
      return "unknown";
  }
}

/** Human-readable label for a Ceph status string. */
export function cephHealthLabel(status: string | undefined | null): string {
  switch (status) {
    case "HEALTH_OK":
      return "Healthy";
    case "HEALTH_WARN":
      return "Warning";
    case "HEALTH_ERR":
      return "Error";
    default:
      return status || "Unknown";
  }
}

const severityRank: Record<CephSeverity, number> = {
  err: 3,
  warn: 2,
  unknown: 1,
  ok: 0,
};

/** Returns the more severe of two severities. */
export function worseSeverity(a: CephSeverity, b: CephSeverity): CephSeverity {
  return severityRank[a] >= severityRank[b] ? a : b;
}

/** True when a severity represents an actionable problem (warn or err). */
export function isProblem(sev: CephSeverity): boolean {
  return sev === "warn" || sev === "err";
}

/** Tailwind classes for a severity status dot. */
export const severityDotClass: Record<CephSeverity, string> = {
  err: "bg-red-500 shadow-[0_0_8px_2px] shadow-red-500/40",
  warn: "bg-amber-500 shadow-[0_0_8px_2px] shadow-amber-500/40",
  unknown: "bg-muted-foreground/50",
  ok: "bg-emerald-500",
};

/** Tailwind text color for a severity. */
export const severityTextClass: Record<CephSeverity, string> = {
  err: "text-red-600 dark:text-red-400",
  warn: "text-amber-600 dark:text-amber-400",
  unknown: "text-muted-foreground",
  ok: "text-emerald-600 dark:text-emerald-400",
};

/** Worst severity across a set of health issues; "ok" when there are none. */
export function worstIssueSeverity(
  issues: { severity: HealthSeverity }[],
): CephSeverity {
  if (issues.some((i) => i.severity === "err")) return "err";
  if (issues.some((i) => i.severity === "warn")) return "warn";
  return "ok";
}
