import type { InventoryRow } from "../types/inventory";

/** The table's quick filter: every row, or the guests in one power state. */
export type QuickStatus = "all" | "running" | "stopped";

export interface InventoryCounts {
  /** VMs and containers that are not templates. */
  guests: number;
  /** Guests that are running. */
  running: number;
  /** Guests that are stopped. */
  stopped: number;
  nodes: number;
  nodesOffline: number;
  templates: number;
}

/** What a set of inventory rows holds, for a page summary and filter counts. */
export function countInventory(rows: readonly InventoryRow[]): InventoryCounts {
  const counts: InventoryCounts = {
    guests: 0,
    running: 0,
    stopped: 0,
    nodes: 0,
    nodesOffline: 0,
    templates: 0,
  };
  for (const row of rows) {
    if (row.type === "node") {
      counts.nodes++;
      if (row.status === "offline") counts.nodesOffline++;
    } else if (row.template) {
      counts.templates++;
    } else {
      counts.guests++;
      if (row.status === "running") counts.running++;
      if (row.status === "stopped") counts.stopped++;
    }
  }
  return counts;
}

/**
 * The rows a quick filter shows. A node is online or offline, never running
 * or stopped, so either state leaves nodes out.
 */
export function filterByQuickStatus(
  rows: readonly InventoryRow[],
  quick: QuickStatus,
): InventoryRow[] {
  return quick === "all"
    ? [...rows]
    : rows.filter((row) => row.status === quick);
}
