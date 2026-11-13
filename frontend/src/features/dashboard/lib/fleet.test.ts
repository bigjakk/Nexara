import { describe, it, expect } from "vitest";
import { buildFleet, fleetGuestFromRow, loadLevel } from "./fleet";
import type { InventoryRow } from "@/features/inventory/types/inventory";
import { makeCluster, makeNode, makeVM } from "@/test/cluster.fixtures";
import type { AggregatedMetrics, VmLiveMetric } from "@/types/ws";
import type { ClusterSummary } from "../api/dashboard-queries";

describe("loadLevel", () => {
  it.each([
    { cpu: null, want: 1 },
    { cpu: 0, want: 1 },
    { cpu: 4.9, want: 1 },
    { cpu: 5, want: 2 },
    { cpu: 24.9, want: 2 },
    { cpu: 25, want: 3 },
    { cpu: 59.9, want: 3 },
    { cpu: 60, want: 4 },
    { cpu: 100, want: 4 },
  ])("CPU $cpu is level $want", ({ cpu, want }) => {
    expect(loadLevel(cpu)).toBe(want);
  });
});

describe("buildFleet", () => {
  const reading = (cpuPercent: number, memPercent: number): VmLiveMetric => ({
    collectedAt: 0,
    cpuPercent,
    memPercent,
    diskReadBps: 0,
    diskWriteBps: 0,
    netInBps: 0,
    netOutBps: 0,
  });
  const summary: ClusterSummary = {
    cluster: makeCluster({ id: "c1", name: "cluster01", pve_version: "9.2" }),
    nodeCount: 2,
    nodesOnline: 1,
    vmCount: 0,
    vmsRunning: 0,
    containerCount: 0,
    containersRunning: 0,
    storageTotalBytes: 1000,
    storageUsedBytes: 250,
  };
  const nodes = new Map([
    [
      "c1",
      [
        makeNode({ id: "n2", name: "pve-02", status: "offline" }),
        makeNode({ id: "n1", name: "pve-01", mem_total: 1000 }),
      ],
    ],
  ]);
  const vms = new Map([
    [
      "c1",
      [
        makeVM({ id: "v3", node_id: "n1", vmid: 300, name: "linux03" }),
        makeVM({ id: "v1", node_id: "n1", vmid: 100, name: "linux01" }),
        makeVM({
          id: "v2",
          node_id: "n1",
          vmid: 200,
          name: "linux02",
          type: "lxc",
          status: "stopped",
        }),
        makeVM({ id: "t1", node_id: "n1", vmid: 900, template: true }),
      ],
    ],
  ]);
  // Only pve-01 and guest 100 have reported; nothing else has a reading yet.
  const metrics = new Map([
    [
      "c1",
      {
        nodeMetrics: new Map([["n1", reading(40, 50)]]),
        vmMetrics: new Map([["v1", reading(70, 10)]]),
      } as AggregatedMetrics,
    ],
  ]);

  const fleet = buildFleet([summary], nodes, vms, metrics);
  const [cluster] = fleet.clusters;
  const [first, second] = cluster?.nodes ?? [];

  it("counts guests without the template, and orders nodes by name", () => {
    expect({
      nodes: fleet.nodeCount,
      guests: fleet.guestCount,
      running: fleet.guestsRunning,
      vms: cluster?.vmCount,
      containers: cluster?.containerCount,
      online: cluster?.nodesOnline,
      storage: cluster?.storagePercent,
      order: cluster?.nodes.map((n) => n.name),
    }).toEqual({
      nodes: 2,
      guests: 3,
      running: 2,
      vms: 2,
      containers: 1,
      online: 1,
      storage: 25,
      order: ["pve-01", "pve-02"],
    });
  });

  it("reads a node's load from its metric and leaves an unreported one null", () => {
    expect({
      cpu: first?.cpuPercent,
      memUsed: first?.memUsedBytes,
      offline: [second?.online, second?.cpuPercent, second?.memUsedBytes],
    }).toEqual({ cpu: 40, memUsed: 500, offline: [false, null, null] });
  });

  it("shades running guests by CPU, in VMID order, and links each to its page", () => {
    expect(
      first?.guests.map((g) => [g.vmid, g.kind, g.cpuPercent, g.load, g.href]),
    ).toEqual([
      [100, "vm", 70, 4, "/inventory/vm/c1/v1"],
      // Stopped: nothing to shade.
      [200, "ct", null, null, "/inventory/ct/c1/v2"],
      // Running with no reading yet: the idle shade, and no invented 0%.
      [300, "vm", null, 1, "/inventory/vm/c1/v3"],
    ]);
  });
});

describe("fleetGuestFromRow", () => {
  const row = (over: Partial<InventoryRow>): InventoryRow =>
    ({
      id: "v1",
      type: "vm",
      name: "linux01",
      status: "running",
      clusterId: "c1",
      vmid: 100,
      template: false,
      cpuPercent: 30,
      ...over,
    }) as InventoryRow;

  it.each([
    {
      name: "a running VM is shaded by its CPU",
      over: {},
      want: { kind: "vm", load: 3, href: "/inventory/vm/c1/v1" },
    },
    {
      name: "a stopped container has nothing to shade",
      over: { type: "ct" as const, status: "stopped" as const },
      want: { kind: "ct", load: null, href: "/inventory/ct/c1/v1" },
    },
  ])("$name", ({ over, want }) => {
    const guest = fleetGuestFromRow(row(over));
    expect({ kind: guest?.kind, load: guest?.load, href: guest?.href }).toEqual(
      want,
    );
  });

  it("has no cell for a template or a node row", () => {
    expect(fleetGuestFromRow(row({ template: true }))).toBeNull();
    expect(fleetGuestFromRow(row({ type: "node" }))).toBeNull();
  });
});
