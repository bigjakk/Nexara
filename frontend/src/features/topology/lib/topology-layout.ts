import type { Node } from "@xyflow/react";
import type { TopologyNodeData } from "./topology-transform";

const CLUSTER_WIDTH = 220;
const HOST_WIDTH = 200;
const GUEST_WIDTH = 160;
const STORAGE_WIDTH = 160;

const HOST_HEIGHT = 80;
const GUEST_HEIGHT = 60;
const STORAGE_HEIGHT = 60;

const HORIZONTAL_GAP = 60;
const VERTICAL_GAP = 80;

interface LayoutOptions {
  direction: "TB" | "LR";
}

/**
 * Hierarchical layout: Cluster -> Hosts -> Guests/Storage.
 * Places nodes in a tree structure with consistent spacing.
 */
export function applyHierarchicalLayout(
  nodes: Node[],
  edges: { source: string; target: string }[],
  options: LayoutOptions = { direction: "TB" },
): Node[] {
  if (nodes.length === 0) return nodes;

  // Build adjacency list
  const children = new Map<string, string[]>();
  const parentOf = new Map<string, string>();
  for (const edge of edges) {
    const list = children.get(edge.source) ?? [];
    list.push(edge.target);
    children.set(edge.source, list);
    parentOf.set(edge.target, edge.source);
  }

  // Find root nodes (clusters -- no parent)
  const roots = nodes
    .filter((n) => !parentOf.has(n.id))
    .map((n) => n.id);

  const positions = new Map<string, { x: number; y: number }>();
  const isHorizontal = options.direction === "LR";

  // Measure subtree widths for centering
  const subtreeWidth = new Map<string, number>();

  function measureWidth(nodeId: string): number {
    const kids = children.get(nodeId) ?? [];
    const nd = nodes.find((n) => n.id === nodeId);
    const nodeData = nd?.data as TopologyNodeData | undefined;
    const nodeWidth = getNodeWidth(nodeData);

    if (kids.length === 0) {
      subtreeWidth.set(nodeId, nodeWidth);
      return nodeWidth;
    }

    const totalChildWidth = kids.reduce(
      (sum, kid) => sum + measureWidth(kid) + HORIZONTAL_GAP,
      -HORIZONTAL_GAP,
    );

    const width = Math.max(nodeWidth, totalChildWidth);
    subtreeWidth.set(nodeId, width);
    return width;
  }

  // Position nodes recursively
  function positionNode(
    nodeId: string,
    x: number,
    y: number,
  ): void {
    const myWidth = subtreeWidth.get(nodeId) ?? 0;
    const nd = nodes.find((n) => n.id === nodeId);
    const nodeData = nd?.data as TopologyNodeData | undefined;
    const nw = getNodeWidth(nodeData);

    if (isHorizontal) {
      positions.set(nodeId, { x: y, y: x + myWidth / 2 - nw / 2 });
    } else {
      positions.set(nodeId, { x: x + myWidth / 2 - nw / 2, y });
    }

    const kids = children.get(nodeId) ?? [];
    if (kids.length === 0) return;

    const totalChildWidth = kids.reduce(
      (sum, kid) => sum + (subtreeWidth.get(kid) ?? 0) + HORIZONTAL_GAP,
      -HORIZONTAL_GAP,
    );

    let childX = x + (myWidth - totalChildWidth) / 2;
    const childY = y + getNodeHeight(nodeData) + VERTICAL_GAP;

    for (const kid of kids) {
      const kidWidth = subtreeWidth.get(kid) ?? 0;
      positionNode(kid, childX, childY);
      childX += kidWidth + HORIZONTAL_GAP;
    }
  }

  // Measure all roots
  for (const root of roots) {
    measureWidth(root);
  }

  // Position roots side by side
  let rootX = 0;
  for (const root of roots) {
    const rootWidth = subtreeWidth.get(root) ?? CLUSTER_WIDTH;
    positionNode(root, rootX, 0);
    rootX += rootWidth + HORIZONTAL_GAP * 2;
  }

  // Apply positions
  return nodes.map((node) => {
    const pos = positions.get(node.id);
    if (pos) {
      return { ...node, position: pos };
    }
    return node;
  });
}

function getNodeWidth(data: TopologyNodeData | undefined): number {
  if (!data) return HOST_WIDTH;
  switch (data.kind) {
    case "cluster":
      return CLUSTER_WIDTH;
    case "host":
      return HOST_WIDTH;
    case "guest":
      return GUEST_WIDTH;
    case "storage":
      return STORAGE_WIDTH;
    default:
      return HOST_WIDTH;
  }
}

function getNodeHeight(data: TopologyNodeData | undefined): number {
  if (!data) return HOST_HEIGHT;
  switch (data.kind) {
    case "cluster":
      return HOST_HEIGHT;
    case "host":
      return HOST_HEIGHT;
    case "guest":
      return GUEST_HEIGHT;
    case "storage":
      return STORAGE_HEIGHT;
    default:
      return HOST_HEIGHT;
  }
}
