/**
 * An audit entry's details blob, as the readers that show it spell it out: the
 * audit page's expanded row and a guest's History tab. One copy, so a key both
 * show is labelled the same in both.
 */

function detailKeyLabel(key: string): string {
  const labels: Record<string, string> = {
    vm_type: "Type",
    vmid: "VMID",
    source_node: "Source Node",
    target_node: "Target Node",
    migration_type: "Migration Type",
    online: "Live Migration",
    error: "Error",
    status: "Status",
    bwlimit_kib: "BW Limit (KiB)",
    delete_source: "Delete Source",
    target_vmid: "Target VMID",
    storage: "Storage",
    pool: "Pool",
    name: "Name",
    size: "Size",
  };
  return (
    labels[key] ??
    key.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase())
  );
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
      pairs.push([detailKeyLabel(k), detailValue(v)]);
    }
    return pairs.length > 0 ? pairs : null;
  } catch {
    return null;
  }
}
