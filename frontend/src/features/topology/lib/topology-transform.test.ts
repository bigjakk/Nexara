import { describe, it, expect } from "vitest";
import {
  buildTopologyGraph,
  getStatusColor,
  getGuestStatusColor,
} from "./topology-transform";
import type { TopologyInput, TopologyFilters } from "./topology-transform";
import { makeStorage as baseStorage } from "@/test/storage.fixtures";
import { makeCluster, makeNode } from "@/test/cluster.fixtures";
import type { VMResponse, StorageResponse } from "@/types/api";

function makeVM(overrides: Partial<VMResponse> = {}): VMResponse {
  return {
    id: "v1",
    cluster_id: "c1",
    node_id: "n1",
    vmid: 100,
    name: "web-01",
    type: "qemu",
    status: "running",
    cpu_count: 4,
    mem_total: 8589934592,
    disk_total: 107374182400,
    uptime: 3600,
    template: false,
    tags: "",
    ha_state: "",
    pool: "",
    ostype: "",
    config_ostype: "",
    last_seen_at: "2024-01-01T00:00:00Z",
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    ...overrides,
  };
}

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

  it("animates edges for online/running resources", () => {
    const graph = buildTopologyGraph(makeInput(), defaultFilters);

    // cluster→host edge: animated (node is online)
    expect(graph.edges[0]?.animated).toBe(true);
    // host→guest edge: animated (vm is running)
    expect(graph.edges[1]?.animated).toBe(true);
  });
});

describe("getStatusColor", () => {
  it("returns green for online", () => {
    expect(getStatusColor("online")).toBe("#10b981");
  });

  it("returns red for offline", () => {
    expect(getStatusColor("offline")).toBe("#ef4444");
  });

  it("returns yellow for warning", () => {
    expect(getStatusColor("warning")).toBe("#f59e0b");
  });

  it("returns gray for unknown status", () => {
    expect(getStatusColor("something")).toBe("#6b7280");
  });
});

describe("getGuestStatusColor", () => {
  it("returns green for running", () => {
    expect(getGuestStatusColor("running")).toBe("#10b981");
  });

  it("returns neutral gray for stopped", () => {
    expect(getGuestStatusColor("stopped")).toBe("#6b7280");
  });

  it("returns yellow for paused", () => {
    expect(getGuestStatusColor("paused")).toBe("#f59e0b");
  });
});

// formatBytes coverage lives in src/lib/format.test.ts; topology re-exports
// it from there, so it follows the app-wide byte-unit preference.
