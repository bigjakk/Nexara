import type { InventoryRow } from "@/features/inventory/types/inventory";

/** The order the counts are said in; any other status follows, by name. */
const STATUS_ORDER = ["running", "paused", "suspended", "stopped"];

/**
 * A node's guests in one line, for its Guests panel: "6 running, 4 stopped,
 * 2 templates". A template is counted apart, since it is never running or
 * stopped in any sense that matters. Empty when the node has no guests.
 */
export function summariseNodeGuests(rows: readonly InventoryRow[]): string {
  const byStatus = new Map<string, number>();
  let templates = 0;
  for (const row of rows) {
    if (row.type === "node") continue;
    if (row.template) {
      templates += 1;
      continue;
    }
    byStatus.set(row.status, (byStatus.get(row.status) ?? 0) + 1);
  }
  const statuses = [...byStatus.keys()].sort((a, b) => {
    const ai = STATUS_ORDER.indexOf(a);
    const bi = STATUS_ORDER.indexOf(b);
    if (ai === -1 && bi === -1) return a.localeCompare(b);
    if (ai === -1) return 1;
    if (bi === -1) return -1;
    return ai - bi;
  });
  const parts = statuses.map(
    (status) => `${String(byStatus.get(status) ?? 0)} ${status}`,
  );
  if (templates > 0) {
    parts.push(
      `${String(templates)} ${templates === 1 ? "template" : "templates"}`,
    );
  }
  return parts.join(", ");
}
