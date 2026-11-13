import { describe, it, expect } from "vitest";
import { buildTopologyGraph, EDGE_NEUTRAL } from "./topology-transform";
import type { TopologyInput, TopologyFilters } from "./topology-transform";
import { makeStorage as baseStorage } from "@/test/storage.fixtures";
import { makeCluster, makeNode, makeVM } from "@/test/cluster.fixtures";
import type { StorageResponse } from "@/types/api";

/**
 * A populated LVM-thin pool. Topology renders capacity, so the shared
 * factory's zeroed defaults would draw every node as empty — but the field
 * LIST still comes from there, so a new StorageResponse field lands in one
 * place instead of three.
 */
function makeStorage(
  overrides: Partial<StorageResponse> = {},
): StorageResponse {
  return baseStorage({
    storage: "local-lvm",
    type: "lvmthin",
    content: "images,rootdir",
    total: 500000000000,
    used: 250000000000,
    avail: 250000000000,
    last_seen_at: "2024-01-01T00:00:00Z",
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    ...overrides,
  });
}

function makeInput(overrides: Partial<TopologyInput> = {}): TopologyInput {
  const cluster = makeCluster();
  const node = makeNode();
  const vm = makeVM();
  const storage = makeStorage();

  return {
    clusters: [cluster],
    nodesByCluster: new Map([["c1", [node]]]),
    vmsByCluster: new Map([["c1", [vm]]]),
    storageByCluster: new Map([["c1", [storage]]]),
    ...overrides,
  };
}

const defaultFilters: TopologyFilters = {
  showVMs: true,
  showStorage: true,
  selectedClusterId: null,
};

describe("buildTopologyGraph", () => {
  it("creates cluster, host, guest, and storage nodes", () => {
    const graph = buildTopologyGraph(makeInput(), defaultFilters);

    expect(graph.nodes).toHaveLength(4);
    expect(graph.nodes.map((n) => n.type)).toEqual([
      "clusterNode",
      "hostNode",
      "guestNode",
      "storageNode",
    ]);
  });

  it("creates edges between cluster→host, host→guest, host→storage", () => {
    const graph = buildTopologyGraph(makeInput(), defaultFilters);

    expect(graph.edges).toHaveLength(3);
    expect(graph.edges[0]?.source).toBe("cluster-c1");
    expect(graph.edges[0]?.target).toBe("host-n1");
    expect(graph.edges[1]?.source).toBe("host-n1");
    expect(graph.edges[1]?.target).toBe("guest-v1");
    expect(graph.edges[2]?.source).toBe("host-n1");
    expect(graph.edges[2]?.target).toBe("storage-s1");
  });

  it("hides VMs when showVMs is false", () => {
    const graph = buildTopologyGraph(makeInput(), {
      ...defaultFilters,
      showVMs: false,
    });

    const guestNodes = graph.nodes.filter((n) => n.type === "guestNode");
    expect(guestNodes).toHaveLength(0);
  });

  it("hides storage when showStorage is false", () => {
    const graph = buildTopologyGraph(makeInput(), {
      ...defaultFilters,
      showStorage: false,
    });

    const storageNodes = graph.nodes.filter((n) => n.type === "storageNode");
    expect(storageNodes).toHaveLength(0);
  });

  it("filters by selectedClusterId", () => {
    const c2 = makeCluster({ id: "c2", name: "other-cluster" });
    const input = makeInput({
      clusters: [makeCluster(), c2],
      nodesByCluster: new Map([
        ["c1", [makeNode()]],
        ["c2", [makeNode({ id: "n2", cluster_id: "c2", name: "pve-node-2" })]],
      ]),
      vmsByCluster: new Map([
        ["c1", []],
        ["c2", []],
      ]),
      storageByCluster: new Map([
        ["c1", []],
        ["c2", []],
      ]),
    });

    const graph = buildTopologyGraph(input, {
      ...defaultFilters,
      showVMs: false,
      showStorage: false,
      selectedClusterId: "c2",
    });

    expect(graph.nodes).toHaveLength(2); // cluster + 1 host
    expect(graph.nodes[0]?.id).toBe("cluster-c2");
  });

  it("deduplicates shared storage across nodes", () => {
    const input = makeInput({
      storageByCluster: new Map([
        [
          "c1",
          [
            makeStorage({
              id: "s1",
              storage: "ceph-pool",
              shared: true,
              node_id: "n1",
            }),
            makeStorage({
              id: "s2",
              storage: "ceph-pool",
              shared: true,
              node_id: "n2",
            }),
          ],
        ],
      ]),
    });

    const graph = buildTopologyGraph(input, defaultFilters);
    const storageNodes = graph.nodes.filter((n) => n.type === "storageNode");
    expect(storageNodes).toHaveLength(1);
  });

  it("connects shared storage to cluster, not host", () => {
    const input = makeInput({
      storageByCluster: new Map([
        ["c1", [makeStorage({ storage: "ceph-pool", shared: true })]],
      ]),
    });

    const graph = buildTopologyGraph(input, defaultFilters);
    const storageEdge = graph.edges.find((e) =>
      e.target.startsWith("storage-shared"),
    );
    expect(storageEdge?.source).toBe("cluster-c1");
  });

  it("returns empty graph for empty input", () => {
    const input = makeInput({
      clusters: [],
      nodesByCluster: new Map(),
      vmsByCluster: new Map(),
      storageByCluster: new Map(),
    });

    const graph = buildTopologyGraph(input, defaultFilters);
    expect(graph.nodes).toHaveLength(0);
    expect(graph.edges).toHaveLength(0);
  });

  // Colour is state: a healthy tree is all neutral, still edges.
  it.each([
    { name: "an online host", node: {}, storage: {}, edge: 0, dashed: false },
    {
      name: "an offline host",
      node: { status: "offline" },
      storage: {},
      edge: 0,
      dashed: true,
    },
    {
      name: "an inactive store",
      node: {},
      storage: { active: false },
      edge: 2,
      dashed: true,
    },
  ])(
    "draws the edge to $name plain or flagged",
    ({ node, storage, edge, dashed }) => {
      const graph = buildTopologyGraph(
        makeInput({
          nodesByCluster: new Map([["c1", [makeNode(node)]]]),
          storageByCluster: new Map([["c1", [makeStorage(storage)]]]),
        }),
        defaultFilters,
      );
      const style = graph.edges[edge]?.style;
      expect(graph.edges[edge]?.animated).toBeUndefined();
      expect(style?.stroke === EDGE_NEUTRAL).toBe(!dashed);
      expect(style?.strokeDasharray !== undefined).toBe(dashed);
    },
  );

  it("leaves templates out of the guest count, the host's cells and the guest chips", () => {
    const graph = buildTopologyGraph(
      makeInput({
        vmsByCluster: new Map([
          ["c1", [makeVM(), makeVM({ id: "v2", vmid: 101, template: true })]],
        ]),
      }),
      defaultFilters,
    );
    expect(graph.nodes[0]?.data).toMatchObject({ vmCount: 1 });
    expect(graph.nodes[1]?.data).toMatchObject({
      guests: [expect.objectContaining({ vmId: "v1" })],
    });
    expect(
      graph.nodes.filter((n) => n.type === "guestNode").map((n) => n.id),
    ).toEqual(["guest-v1"]);
  });
});

// formatBytes coverage lives in src/lib/format.test.ts; topology re-exports
// it from there, so it follows the app-wide byte-unit preference.
