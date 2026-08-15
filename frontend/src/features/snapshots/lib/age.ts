import type { GuestSnapshotRow } from "../types/snapshots";

/** Stable row identity for expansion/confirm state. */
export function snapshotRowKey(row: GuestSnapshotRow): string {
  return `${row.cluster_id}:${String(row.vmid)}:${row.name}`;
}

/** Age in fractional days, or null when Proxmox omitted snaptime.
 * Never compute from 0 — that would read as ~56 years. */
export function ageDays(snapTime: number, nowMs: number): number | null {
  if (snapTime <= 0) return null;
  return (nowMs / 1000 - snapTime) / 86400;
}

export type AgeBucket = "fresh" | "week" | "month" | "unknown";

/** Buckets match the stat cards and the age filter: >30d, >7d, else fresh. */
export function ageBucket(days: number | null): AgeBucket {
  if (days === null) return "unknown";
  if (days > 30) return "month";
  if (days > 7) return "week";
  return "fresh";
}

export function formatAge(days: number | null): string {
  if (days === null) return "—";
  if (days < 1) {
    const hours = Math.floor(days * 24);
    return hours < 1 ? "<1h" : `${String(hours)}h`;
  }
  return `${String(Math.floor(days))}d`;
}
