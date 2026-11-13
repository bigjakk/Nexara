import { describe, expect, it } from "vitest";
import type { InventoryRow } from "@/features/inventory/types/inventory";
import { summariseNodeGuests } from "./node-guest-summary";

function row(over: Partial<InventoryRow>): InventoryRow {
  return {
    key: "",
    id: "",
    type: "vm",
    name: "linux01",
    status: "running",
    clusterName: "cluster01",
    clusterId: "c1",
    nodeName: "pve-01",
    vmid: 100,
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

describe("summariseNodeGuests", () => {
  it.each<[string, InventoryRow[], string]>([
    ["no guests", [], ""],
    [
      "running before stopped, whatever order they arrive in",
      [row({ status: "stopped" }), row({}), row({ type: "ct" })],
      "2 running, 1 stopped",
    ],
    [
      "templates apart from the states, and a node row not at all",
      [
        row({}),
        row({ status: "stopped", template: true }),
        row({ type: "node", status: "online" }),
      ],
      "1 running, 1 template",
    ],
    [
      "a state between running and stopped, then one it has no place for",
      [row({ status: "unknown" }), row({ status: "paused" }), row({})],
      "1 running, 1 paused, 1 unknown",
    ],
  ])("%s", (_name, rows, expected) => {
    expect(summariseNodeGuests(rows)).toBe(expected);
  });
});
