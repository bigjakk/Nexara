import { describe, it, expect } from "vitest";
import { applyHierarchicalLayout } from "./topology-layout";
import type { Node, Edge } from "@xyflow/react";
import type { ClusterNodeData, HostNodeData, GuestNodeData } from "./topology-transform";

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
      } satisfies HostNodeData),
    ];
    const edges: Edge[] = [
      { id: "e1", source: "cluster-1", target: "host-1" },
    ];

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
      } satisfies HostNodeData),
    ];
    const edges: Edge[] = [
      { id: "e1", source: "cluster-1", target: "host-1" },
    ];

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

    expect(hostPos.y).toBeGreaterThan(clusterPos.y);
    expect(guestPos.y).toBeGreaterThan(hostPos.y);
  });
});
