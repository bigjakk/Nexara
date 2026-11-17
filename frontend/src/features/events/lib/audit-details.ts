/**
 * An audit entry's details blob, as the readers that show it spell it out: the
 * audit page's expanded row and a guest's History tab. One copy, so a key both
 * show is labelled the same in both.
 */

import { formatDate } from "@/lib/format";
import { spellOutKey } from "@/lib/spell-out-key";

function detailKeyLabel(key: string): string {
  const labels: Record<string, string> = {
    vm_type: "Type",
    vmid: "VMID",
    source_node: "Source node",
    target_node: "Target node",
    migration_type: "Migration type",
    online: "Live migration",
    error: "Error",
    status: "Status",
    bwlimit_kib: "BW limit (KiB)",
    delete_source: "Delete source",
    target_vmid: "Target VMID",
    storage: "Storage",
    pool: "Pool",
    name: "Name",
    size: "Size",
    // The links between the tasks Proxmox runs as one action
    // (queries/proxmox_task_sync.sql).
    ha_worker_upid: "HA worker task",
    ha_request_upid: "HA request",
    ha_requested_by: "Requested by",
    migration_upid: "Part of migration",
    target_start_upid: "Start on target",
    // A Proxmox user's or token's expiry (handlers/access.go).
    expire: "Expires",
  };
  return labels[key] ?? spellOutKey(key);
}

function detailValue(val: unknown): string {
  if (typeof val === "boolean") return val ? "Yes" : "No";
  if (typeof val === "number") return String(val);
  if (typeof val === "string") return val;
  if (val === null || val === undefined) return "-";
  return JSON.stringify(val);
}

/** The details as label/value pairs in recorded order; null when there are none. */
export function detailPairs(details: string): Array<[string, string]> | null {
  if (!details || details === "{}" || details === "null") return null;
  try {
    const d = JSON.parse(details) as Record<string, unknown>;
    const pairs: Array<[string, string]> = [];
    for (const [k, v] of Object.entries(d)) {
      pairs.push([
        detailKeyLabel(k),
        // Proxmox's epoch seconds, 0 for never, as the access tables show it.
        k === "expire" && typeof v === "number"
          ? formatDate(v * 1000, "Never")
          : detailValue(v),
      ]);
    }
    return pairs.length > 0 ? pairs : null;
  } catch {
    return null;
  }
}
