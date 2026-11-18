import { spellOutKey } from "@/lib/spell-out-key";

/** A count and its noun: "1 source", "3 sources". */
export function plural(count: number, noun: string): string {
  return `${String(count)} ${noun}${count === 1 ? "" : "s"}`;
}

/**
 * A job list in one clause for a page summary: "5 imports, 2 in progress,
 * 1 failed". A part with nothing to say is left out, and an empty list is
 * null so the page can say what it is for instead.
 */
export function jobSummary(
  noun: string,
  statuses: readonly string[],
  /** The statuses that mean a job has not finished yet. */
  activeStatuses: readonly string[],
): string | null {
  if (statuses.length === 0) return null;
  const active = statuses.filter((s) => activeStatuses.includes(s)).length;
  const failed = statuses.filter((s) => s === "failed").length;
  return [
    plural(statuses.length, noun),
    ...(active > 0 ? [`${String(active)} in progress`] : []),
    ...(failed > 0 ? [`${String(failed)} failed`] : []),
  ].join(", ");
}

/** A migration job that has not finished: queued, being checked, or moving. */
export const ACTIVE_MIGRATION_STATUSES = [
  "pending",
  "checking",
  "migrating",
] as const;

const CHECK_LABELS = new Map([
  ["target_node_online", "Target node"],
  ["target_vmid", "Target VMID"],
  ["pve_version", "Proxmox VE version"],
  ["cpu_compatibility", "CPU compatibility"],
  ["storage_mappings", "Storage mappings"],
  ["network_mappings", "Network mappings"],
]);

/**
 * A pre-flight check's name as a label, the message under it saying how it
 * went. The dialogs printed the server's id ("target_node_online").
 */
export function checkLabel(name: string): string {
  return CHECK_LABELS.get(name) ?? spellOutKey(name);
}
