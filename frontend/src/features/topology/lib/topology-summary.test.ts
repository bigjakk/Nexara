import { describe, expect, it } from "vitest";
import { makeCluster, makeNode, makeVM } from "@/test/cluster.fixtures";
import { makeStorage } from "@/test/storage.fixtures";
import { topologyCounts } from "./topology-summary";
import type { TopologyInput } from "./topology-transform";

const input: TopologyInput = {
  clusters: [
    makeCluster({ id: "c1", name: "cluster01" }),
    makeCluster({ id: "c2", name: "cluster02" }),
  ],
  nodesByCluster: new Map([
    [
      "c1",
      [
        makeNode({ id: "n1", name: "pve-01" }),
        makeNode({ id: "n2", name: "pve-02", status: "offline" }),
      ],
    ],
    ["c2", [makeNode({ id: "n3", name: "pve-03" })]],
  ]),
  vmsByCluster: new Map([
    [
      "c1",
      [
        makeVM({ id: "v1", name: "linux01" }),
        makeVM({ id: "v2", name: "linux02", template: true }),
      ],
    ],
    ["c2", [makeVM({ id: "v3", name: "linux03" })]],
  ]),
  storageByCluster: new Map([
    [
      "c1",
      [
        // One shared store seen from both nodes, and a local one on each.
        makeStorage({ id: "s1", storage: "store01", shared: true }),
        makeStorage({ id: "s2", storage: "store01", shared: true }),
        makeStorage({ id: "s3", storage: "local" }),
        makeStorage({ id: "s4", storage: "local" }),
      ],
    ],
  ]),
};

describe("topologyCounts", () => {
  it.each([
    {
      name: "every cluster",
      selected: null,
      want: { clusters: 2, nodes: 3, nodesOffline: 1, guests: 2, storage: 3 },
    },
    {
      name: "the cluster the map is filtered to",
      selected: "c2",
      want: { clusters: 1, nodes: 1, nodesOffline: 0, guests: 1, storage: 0 },
    },
  ])("counts $name", ({ selected, want }) => {
    expect(topologyCounts(input, selected)).toEqual(want);
  });
});
