import { describe, expect, it } from "vitest";
import { countInventory, filterByQuickStatus } from "./inventory-summary";
import type { InventoryRow } from "../types/inventory";

function row(
  key: string,
  over: Pick<InventoryRow, "type" | "status"> & { template?: boolean },
): InventoryRow {
  return {
    key,
    id: key,
    name: key,
    clusterName: "cluster01",
    clusterId: "c1",
    nodeName: "pve-01",
    vmid: over.type === "node" ? null : 100,
    cpuCount: 2,
    memTotal: 0,
    diskTotal: 0,
    uptime: 0,
    tags: "",
    haState: "",
    pool: "",
    template: false,
    ostype: "",
    configOstype: "",
    cpuPercent: null,
    memPercent: null,
    diskReadBps: null,
    diskWriteBps: null,
    netInBps: null,
    netOutBps: null,
    ...over,
  };
}

const ROWS = [
  row("vm-running", { type: "vm", status: "running" }),
  row("ct-running", { type: "ct", status: "running" }),
  row("vm-stopped", { type: "vm", status: "stopped" }),
  row("vm-paused", { type: "vm", status: "paused" }),
  row("template", { type: "vm", status: "stopped", template: true }),
  row("node-online", { type: "node", status: "online" }),
  row("node-offline", { type: "node", status: "offline" }),
];

describe("countInventory", () => {
  it("counts guests, their power states, nodes and templates apart", () => {
    expect(countInventory(ROWS)).toEqual({
      guests: 4,
      running: 2,
      stopped: 1,
      nodes: 2,
      nodesOffline: 1,
      templates: 1,
    });
  });
});

describe("filterByQuickStatus", () => {
  // Nodes are online or offline, so neither power state shows one; a paused
  // guest is in neither; a template is stopped and is listed as such.
  it.each([
    ["all", ROWS.map((r) => r.key)],
    ["running", ["vm-running", "ct-running"]],
    ["stopped", ["vm-stopped", "template"]],
  ] as const)("%s", (quick, want) => {
    expect(filterByQuickStatus(ROWS, quick).map((r) => r.key)).toEqual(want);
  });
});
