import { Position } from "@xyflow/react";
import type { Edge, Node } from "@xyflow/react";
import type { TopologyNodeData } from "./topology-transform";

export const CLUSTER_WIDTH = 220;
export const HOST_WIDTH = 200;
/** A host card's nominal height: what the power overlay centres on. */
export const HOST_HEIGHT = 150;
export const HORIZONTAL_GAP = 60;
const VERTICAL_GAP = 80;

/** A cluster card is two fixed lines. */
const CLUSTER_CARD_HEIGHT = 64;
/** A host card grows with its guests' cells: the room the tallest takes. */
const HOST_SLOT = 176;
const HOST_GAP = 44;
/** Storage and guest chips are one fixed size, so a stack of them is exact. */
export const LEAF_WIDTH = 172;
export const LEAF_HEIGHT = 54;
const LEAF_GAP = 10;
/**
 * The gap between a stack of chips and what it hangs off. The edges into a
 * stack share a trunk down the middle of it.
 */
export const STACK_INSET = 28;
/** From a cluster's card down to its hosts, with nothing in between. */
const CLUSTER_DROP = 56;

const LR_DEPTH_GAP = 72;
const LR_SIBLING_GAP = 24;
const ROOT_GAP_X = 96;
const ROOT_GAP_Y = 72;
/** Left-right, the room the power overlay's UPS lane takes before a cluster. */
const LR_LANE = 220 + HORIZONTAL_GAP * 2;
/** The canvas the clusters are packed for: wider than it is tall. */
const CANVAS_ASPECT = 1.6;

interface LayoutOptions {
  direction: "TB" | "LR";
  /**
   * Leave room for the power overlay's UPSes: under each cluster's card
   * top-down, before each cluster left-right. Off, the tree closes up.
   */
  reserveLane?: boolean;
}

interface Box {
  w: number;
  h: number;
  place: (x: number, y: number) => void;
}

type Kind = TopologyNodeData["kind"] | undefined;

const isBranch = (kind: Kind) => kind === "cluster" || kind === "host";
const isLeafType = (type: string | undefined) =>
  type === "storageNode" || type === "guestNode";

function widthOf(kind: Kind): number {
  if (kind === "cluster") return CLUSTER_WIDTH;
  if (kind === "guest" || kind === "storage") return LEAF_WIDTH;
  return HOST_WIDTH;
}

const stackHeight = (count: number) =>
  count === 0 ? 0 : count * LEAF_HEIGHT + (count - 1) * LEAF_GAP;

/**
 * How many rows to split `boxes` into, in order, so the whole drawing fits a
 * canvas of `aspect` at the largest scale. Fewer rows win a tie.
 */
export function bestRowCount(
  boxes: readonly { w: number; h: number }[],
  aspect = CANVAS_ASPECT,
): number {
  let best = 1;
  let bestScale = 0;
  for (let rows = 1; rows <= boxes.length; rows++) {
    const perRow = Math.ceil(boxes.length / rows);
    let width = 0;
    let height = 0;
    for (let start = 0; start < boxes.length; start += perRow) {
      const row = boxes.slice(start, start + perRow);
      width = Math.max(
        width,
        row.reduce((sum, b) => sum + b.w, 0) + (row.length - 1) * ROOT_GAP_X,
      );
      height += Math.max(...row.map((b) => b.h)) + (start > 0 ? ROOT_GAP_Y : 0);
    }
    const scale = Math.min(aspect / width, 1 / height);
    if (scale > bestScale * 1.001) {
      best = rows;
      bestScale = scale;
    }
  }
  return best;
}

/**
 * Lays the graph out as a tree: cluster, its hosts, and what each holds.
 *
 * Storage and guests are chips stacked beside what holds them, not spread in
 * a row of their own: a host's to its right, a cluster's shared storage after
 * its last host. That keeps a cluster about as wide and as tall as its hosts,
 * so the map is drawn large enough to read. Top-down the hosts run across
 * under their cluster; left-right they run down beside it. Either way the
 * clusters wrap into rows to suit the canvas.
 *
 * Each node also gets the sides its edges attach to.
 */
export function applyHierarchicalLayout(
  nodes: Node[],
  edges: { source: string; target: string }[],
  options: LayoutOptions = { direction: "TB" },
): Node[] {
  if (nodes.length === 0) return nodes;
  const across = options.direction === "LR";
  const reserveLane = options.reserveLane === true;

  const children = new Map<string, string[]>();
  const hasParent = new Set<string>();
  for (const edge of edges) {
    const list = children.get(edge.source) ?? [];
    list.push(edge.target);
    children.set(edge.source, list);
    hasParent.add(edge.target);
  }
  const kindOf = new Map<string, Kind>(
    nodes.map((n) => [n.id, (n.data as TopologyNodeData | undefined)?.kind]),
  );
  const roots = nodes.filter((n) => !hasParent.has(n.id)).map((n) => n.id);

  const positions = new Map<string, { x: number; y: number }>();

  function placeStack(leaves: string[], x: number, top: number): void {
    leaves.forEach((leaf, i) => {
      positions.set(leaf, { x, y: top + i * (LEAF_HEIGHT + LEAF_GAP) });
    });
  }

  function layoutDown(id: string): Box {
    const kind = kindOf.get(id);
    const nodeW = widthOf(kind);
    const kids = children.get(id) ?? [];
    const branches = kids.filter((k) => isBranch(kindOf.get(k)));
    const leaves = kids.filter((k) => !isBranch(kindOf.get(k)));
    const stackW = leaves.length > 0 ? STACK_INSET + LEAF_WIDTH : 0;

    if (branches.length === 0 && kind !== "cluster") {
      // A host, its chips stacked to its right.
      return {
        w: nodeW + stackW,
        h: isBranch(kind)
          ? Math.max(HOST_SLOT, stackHeight(leaves.length))
          : LEAF_HEIGHT,
        place: (x, y) => {
          positions.set(id, { x, y });
          placeStack(leaves, x + nodeW + STACK_INSET, y);
        },
      };
    }

    // A cluster: its hosts in a row under it, then its shared storage as one
    // more stack at the end of that row.
    const boxes = branches.map(layoutDown);
    const branchesW =
      boxes.reduce((sum, b) => sum + b.w, 0) +
      Math.max(0, boxes.length - 1) * HOST_GAP;
    const stackAt = branchesW + (boxes.length > 0 ? HOST_GAP : 0);
    // The cluster sits over its hosts, not over the stack beside them.
    const span = boxes.length > 0 ? branchesW : stackW;
    const lead = Math.max(0, (nodeW - span) / 2);
    const rowY =
      kids.length === 0
        ? 0
        : reserveLane
          ? HOST_HEIGHT + VERTICAL_GAP
          : CLUSTER_CARD_HEIGHT + CLUSTER_DROP;
    return {
      w: Math.max(lead + stackAt + stackW, nodeW),
      h: Math.max(
        CLUSTER_CARD_HEIGHT,
        rowY +
          Math.max(0, ...boxes.map((b) => b.h), stackHeight(leaves.length)),
      ),
      place: (x, y) => {
        positions.set(id, { x: x + lead + span / 2 - nodeW / 2, y });
        let at = x + lead;
        for (const box of boxes) {
          box.place(at, y + rowY);
          at += box.w + HOST_GAP;
        }
        placeStack(leaves, x + lead + stackAt + STACK_INSET, y + rowY);
      },
    };
  }

  function layoutRight(id: string): Box {
    const kind = kindOf.get(id);
    const nodeW = widthOf(kind);
    const cardH =
      kind === "cluster"
        ? CLUSTER_CARD_HEIGHT
        : isBranch(kind)
          ? HOST_HEIGHT
          : LEAF_HEIGHT;
    const slotH = kind === "host" ? HOST_SLOT : cardH;
    const kids = children.get(id) ?? [];
    if (kids.length === 0) {
      return {
        w: nodeW,
        h: slotH,
        place: (x, y) => positions.set(id, { x, y }),
      };
    }
    const boxes = kids.map(layoutRight);
    const gapAfter = (i: number) =>
      isBranch(kindOf.get(kids[i] ?? "")) ||
      isBranch(kindOf.get(kids[i + 1] ?? ""))
        ? LR_SIBLING_GAP
        : LEAF_GAP;
    const kidsH = boxes.reduce(
      (sum, b, i) => sum + b.h + (i < boxes.length - 1 ? gapAfter(i) : 0),
      0,
    );
    const h = Math.max(slotH, kidsH);
    const step = isBranch(kindOf.get(kids[0] ?? ""))
      ? LR_DEPTH_GAP
      : STACK_INSET;
    return {
      w: nodeW + step + Math.max(...boxes.map((b) => b.w)),
      h,
      place: (x, y) => {
        positions.set(id, { x, y: y + (h - cardH) / 2 });
        let at = y + (h - kidsH) / 2;
        boxes.forEach((box, i) => {
          box.place(x + nodeW + step, at);
          at += box.h + gapAfter(i);
        });
      },
    };
  }

  const lane = across && reserveLane ? LR_LANE : 0;
  const boxes = roots.map((root): Box => {
    const box = across ? layoutRight(root) : layoutDown(root);
    return lane === 0
      ? box
      : {
          w: box.w + lane,
          h: box.h,
          place: (x, y) => {
            box.place(x + lane, y);
          },
        };
  });
  const perRow = Math.max(1, Math.ceil(boxes.length / bestRowCount(boxes)));
  let y = 0;
  for (let start = 0; start < boxes.length; start += perRow) {
    const row = boxes.slice(start, start + perRow);
    let x = 0;
    for (const box of row) {
      box.place(x, y);
      x += box.w + ROOT_GAP_X;
    }
    y += Math.max(...row.map((b) => b.h)) + ROOT_GAP_Y;
  }

  return nodes.map((node) => {
    const position = positions.get(node.id);
    if (!position) return node;
    const kind = kindOf.get(node.id);
    return {
      ...node,
      position,
      // A host's chips are always to its right, and a chip is always entered
      // from its left: only a cluster's foot and a host's head turn.
      sourcePosition:
        across || kind === "host" ? Position.Right : Position.Bottom,
      targetPosition: across || !isBranch(kind) ? Position.Left : Position.Top,
    };
  });
}

/**
 * The edges of a top-down layout, with each one from a cluster into its stack
 * of shared storage drawn as a "stack" edge: along a bus level with the
 * cluster's turn toward its hosts, down a trunk beside the stack, and into
 * the chip's side. (A host's own chips need none: they are beside it.)
 */
export function withStackEdges(nodes: Node[], edges: Edge[]): Edge[] {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const stackTop = new Map<string, number>();
  const stacked = (edge: Edge) => {
    const target = byId.get(edge.target);
    return byId.get(edge.source)?.type === "clusterNode" &&
      isLeafType(target?.type)
      ? target
      : undefined;
  };
  for (const edge of edges) {
    const target = stacked(edge);
    if (!target) continue;
    stackTop.set(
      edge.source,
      Math.min(stackTop.get(edge.source) ?? Infinity, target.position.y),
    );
  }
  return edges.map((edge) => {
    if (!stacked(edge)) return edge;
    const top = stackTop.get(edge.source) ?? 0;
    const foot = (byId.get(edge.source)?.position.y ?? 0) + CLUSTER_CARD_HEIGHT;
    return {
      ...edge,
      type: "stack",
      data: { ...edge.data, drop: (top - foot) / 2 },
    };
  });
}
