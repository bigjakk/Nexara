import { describe, it, expect } from "vitest";
import { makeCluster, makeVM } from "@/test/cluster.fixtures";
import type { ClusterSummary } from "../api/dashboard-queries";
import { estateCounts, guestCounts } from "./estate-summary";

const summary = (
  id: string,
  nodeCount: number,
  nodesOnline: number,
): ClusterSummary => ({
  cluster: makeCluster({ id, name: id }),
  nodeCount,
  nodesOnline,
  vmCount: 0,
  vmsRunning: 0,
  containerCount: 0,
  containersRunning: 0,
  storageTotalBytes: 0,
  storageUsedBytes: 0,
});

const guests = new Map([
  [
    "cluster01",
    [
      makeVM({ id: "v1", name: "linux01" }),
      makeVM({ id: "v2", name: "linux02", status: "stopped" }),
      makeVM({ id: "v3", name: "linux03", template: true }),
    ],
  ],
  ["cluster02", [makeVM({ id: "v4", name: "linux04", type: "lxc" })]],
]);

describe("estateCounts", () => {
  it.each([
    {
      name: "nothing registered",
      summaries: [],
      vms: guests,
      want: {
        clusters: 0,
        nodes: 0,
        nodesOffline: 0,
        guests: 0,
        guestsRunning: 0,
      },
    },
    {
      name: "two clusters, a node down, a template left out",
      summaries: [summary("cluster01", 3, 2), summary("cluster02", 1, 1)],
      vms: guests,
      want: {
        clusters: 2,
        nodes: 4,
        nodesOffline: 1,
        guests: 3,
        guestsRunning: 2,
      },
    },
    {
      name: "guest lists not loaded",
      summaries: [summary("cluster01", 3, 3)],
      vms: undefined,
      want: {
        clusters: 1,
        nodes: 3,
        nodesOffline: 0,
        guests: 0,
        guestsRunning: 0,
      },
    },
  ])("$name", ({ summaries, vms, want }) => {
    expect(estateCounts(summaries, vms)).toEqual(want);
  });
});

describe("guestCounts", () => {
  it("counts VMs and containers across lists, leaving templates out", () => {
    expect(guestCounts(guests.values())).toEqual({
      vms: 2,
      vmsRunning: 1,
      containers: 1,
      containersRunning: 1,
    });
  });
});
