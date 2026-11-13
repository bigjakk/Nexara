import { describe, it, expect } from "vitest";
import {
  applyHierarchicalLayout,
  bestRowCount,
  HOST_WIDTH,
  LEAF_WIDTH,
  STACK_INSET,
  withStackEdges,
} from "./topology-layout";
import type { Node, Edge } from "@xyflow/react";
import type {
  ClusterNodeData,
  HostNodeData,
  GuestNodeData,
} from "./topology-transform";

function makeFlowNode(
  id: string,
  type: string,
  data: Record<string, unknown>,
): Node {
  return { id, type, position: { x: 0, y: 0 }, data };
}

function getPosition(result: Node[], id: string): { x: number; y: number } {
  const node = result.find((n) => n.id === id);
  expect(node).toBeDefined();
  return node?.position ?? { x: 0, y: 0 };
}

describe("applyHierarchicalLayout", () => {
  it("returns empty array for empty input", () => {
    const result = applyHierarchicalLayout([], []);
    expect(result).toHaveLength(0);
  });

  it("positions a single root node", () => {
    const nodes: Node[] = [
      makeFlowNode("cluster-1", "clusterNode", {
        kind: "cluster",
        label: "test",
        isActive: true,
        status: "online",
        nodeCount: 0,
        vmCount: 0,
        pveVersion: "",
        clusterId: "1",
      } satisfies ClusterNodeData),
    ];

    const result = applyHierarchicalLayout(nodes, []);
    expect(result).toHaveLength(1);
    const pos = getPosition(result, "cluster-1");
    expect(pos.x).toBe(0);
    expect(pos.y).toBe(0);
  });

  it("positions children below parent in TB direction", () => {
    const nodes: Node[] = [
      makeFlowNode("cluster-1", "clusterNode", {
        kind: "cluster",
        label: "test",
        isActive: true,
        status: "online",
        nodeCount: 1,
        vmCount: 0,
        pveVersion: "",
        clusterId: "1",
      } satisfies ClusterNodeData),
      makeFlowNode("host-1", "hostNode", {
        kind: "host",
        label: "pve1",
        status: "online",
        cpuCount: 8,
        memTotal: 16000000000,
        pveVersion: "8.0",
        clusterId: "1",
        nodeId: "1",
        guests: [],
      } satisfies HostNodeData),
    ];
    const edges: Edge[] = [{ id: "e1", source: "cluster-1", target: "host-1" }];

    const result = applyHierarchicalLayout(nodes, edges, {
      direction: "TB",
    });

    const clusterPos = getPosition(result, "cluster-1");
    const hostPos = getPosition(result, "host-1");

    // Host should be below cluster
    expect(hostPos.y).toBeGreaterThan(clusterPos.y);
  });

  it("positions children to the right of parent in LR direction", () => {
    const nodes: Node[] = [
      makeFlowNode("cluster-1", "clusterNode", {
        kind: "cluster",
        label: "test",
        isActive: true,
        status: "online",
        nodeCount: 1,
        vmCount: 0,
        pveVersion: "",
        clusterId: "1",
      } satisfies ClusterNodeData),
      makeFlowNode("host-1", "hostNode", {
        kind: "host",
        label: "pve1",
        status: "online",
        cpuCount: 8,
        memTotal: 16000000000,
        pveVersion: "8.0",
        clusterId: "1",
        nodeId: "1",
        guests: [],
      } satisfies HostNodeData),
    ];
    const edges: Edge[] = [{ id: "e1", source: "cluster-1", target: "host-1" }];

    const result = applyHierarchicalLayout(nodes, edges, {
      direction: "LR",
    });

    const clusterPos = getPosition(result, "cluster-1");
    const hostPos = getPosition(result, "host-1");

    // Host should be to the right of cluster
    expect(hostPos.x).toBeGreaterThan(clusterPos.x);
  });

  it("spreads multiple siblings horizontally in TB mode", () => {
    const nodes: Node[] = [
      makeFlowNode("cluster-1", "clusterNode", {
        kind: "cluster",
        label: "test",
        isActive: true,
        status: "online",
        nodeCount: 2,
        vmCount: 0,
        pveVersion: "",
        clusterId: "1",
      } satisfies ClusterNodeData),
      makeFlowNode("host-1", "hostNode", {
        kind: "host",
        label: "pve1",
        status: "online",
        cpuCount: 8,
        memTotal: 16000000000,
        pveVersion: "8.0",
        clusterId: "1",
        nodeId: "h1",
        guests: [],
      } satisfies HostNodeData),
      makeFlowNode("host-2", "hostNode", {
        kind: "host",
        label: "pve2",
        status: "online",
        cpuCount: 8,
        memTotal: 16000000000,
        pveVersion: "8.0",
        clusterId: "1",
        nodeId: "h2",
        guests: [],
      } satisfies HostNodeData),
    ];
    const edges: Edge[] = [
      { id: "e1", source: "cluster-1", target: "host-1" },
      { id: "e2", source: "cluster-1", target: "host-2" },
    ];

    const result = applyHierarchicalLayout(nodes, edges, {
      direction: "TB",
    });

    const host1Pos = getPosition(result, "host-1");
    const host2Pos = getPosition(result, "host-2");

    // Siblings should be at the same Y but different X
    expect(host1Pos.y).toBe(host2Pos.y);
    expect(host1Pos.x).not.toBe(host2Pos.x);
  });

  it("handles three-level hierarchy: cluster -> host -> guest", () => {
    const nodes: Node[] = [
      makeFlowNode("cluster-1", "clusterNode", {
        kind: "cluster",
        label: "test",
        isActive: true,
        status: "online",
        nodeCount: 1,
        vmCount: 1,
        pveVersion: "",
        clusterId: "1",
      } satisfies ClusterNodeData),
      makeFlowNode("host-1", "hostNode", {
        kind: "host",
        label: "pve1",
        status: "online",
        cpuCount: 8,
        memTotal: 16000000000,
        pveVersion: "8.0",
        clusterId: "1",
        nodeId: "h1",
        guests: [],
      } satisfies HostNodeData),
      makeFlowNode("guest-1", "guestNode", {
        kind: "guest",
        label: "vm-1",
        vmid: 100,
        type: "qemu",
        status: "running",
        cpuCount: 2,
        memTotal: 4000000000,
        haState: "",
        clusterId: "1",
        vmId: "g1",
      } satisfies GuestNodeData),
    ];
    const edges: Edge[] = [
      { id: "e1", source: "cluster-1", target: "host-1" },
      { id: "e2", source: "host-1", target: "guest-1" },
    ];

    const result = applyHierarchicalLayout(nodes, edges, {
      direction: "TB",
    });

    const clusterPos = getPosition(result, "cluster-1");
    const hostPos = getPosition(result, "host-1");
    const guestPos = getPosition(result, "guest-1");

    // The host is under its cluster, and the guest's chip beside the host.
    expect(hostPos.y).toBeGreaterThan(clusterPos.y);
    expect(guestPos.x).toBeGreaterThan(hostPos.x);
    expect(guestPos.y).toBe(hostPos.y);
  });
});

describe("stacked chips", () => {
  const node = (id: string, type: string, kind: string): Node =>
    makeFlowNode(id, type, { kind });
  // cluster01 holds two hosts and a shared store; the first host has two
  // stores of its own.
  const nodes = [
    node("cluster-1", "clusterNode", "cluster"),
    node("host-1", "hostNode", "host"),
    node("host-2", "hostNode", "host"),
    node("local-1", "storageNode", "storage"),
    node("local-2", "storageNode", "storage"),
    node("shared-1", "storageNode", "storage"),
  ];
  const edges: Edge[] = [
    { id: "e1", source: "cluster-1", target: "host-1" },
    { id: "e2", source: "cluster-1", target: "host-2" },
    { id: "e3", source: "host-1", target: "local-1" },
    { id: "e4", source: "host-1", target: "local-2" },
    { id: "e5", source: "cluster-1", target: "shared-1" },
  ];

  it("stacks a host's chips beside it, and the shared storage after the last host", () => {
    const result = applyHierarchicalLayout(nodes, edges);
    const host1 = getPosition(result, "host-1");
    const host2 = getPosition(result, "host-2");
    const first = getPosition(result, "local-1");
    const second = getPosition(result, "local-2");
    const shared = getPosition(result, "shared-1");

    expect(first).toEqual({
      x: host1.x + HOST_WIDTH + STACK_INSET,
      y: host1.y,
    });
    expect(second.x).toBe(first.x);
    expect(second.y).toBeGreaterThan(first.y);
    // The next host starts clear of the stack, in the same row.
    expect(host2.x).toBeGreaterThan(first.x + LEAF_WIDTH);
    expect(host2.y).toBe(host1.y);
    expect(shared.x).toBeGreaterThan(host2.x + HOST_WIDTH);
    expect(shared.y).toBe(host1.y);
    // Only the cluster's own stack is reached by the bus-and-trunk edge.
    expect(withStackEdges(result, edges).map((e) => e.type ?? "plain")).toEqual(
      ["plain", "plain", "plain", "plain", "stack"],
    );
  });

  it.each([
    { reserveLane: false, drop: 120 },
    { reserveLane: true, drop: 230 },
  ])(
    "drops the hosts $drop below their cluster with reserveLane $reserveLane",
    ({ reserveLane, drop }) => {
      const result = applyHierarchicalLayout(nodes, edges, {
        direction: "TB",
        reserveLane,
      });
      expect(
        getPosition(result, "host-1").y - getPosition(result, "cluster-1").y,
      ).toBe(drop);
    },
  );
});

describe("bestRowCount", () => {
  const wide = { w: 1500, h: 300 };
  const tall = { w: 400, h: 900 };
  it.each([
    { name: "one cluster", boxes: [wide], rows: 1 },
    {
      name: "two wide clusters, one above the other",
      boxes: [wide, wide],
      rows: 2,
    },
    {
      name: "four wide clusters as a column",
      boxes: [wide, wide, wide, wide],
      rows: 4,
    },
    { name: "two tall clusters side by side", boxes: [tall, tall], rows: 1 },
    {
      name: "four tall clusters in one row",
      boxes: [tall, tall, tall, tall],
      rows: 1,
    },
  ])("packs $name", ({ boxes, rows }) => {
    expect(bestRowCount(boxes)).toBe(rows);
  });
});
