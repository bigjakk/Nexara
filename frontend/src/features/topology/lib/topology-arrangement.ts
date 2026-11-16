import type { Node, NodeChange, XYPosition } from "@xyflow/react";

/**
 * Where the person has dragged nodes to, by node id. The graph is laid out
 * again whenever a read refreshes (every 30 s or so), and that layout put
 * every dragged node back; these positions are put over it instead, until
 * the person resets the layout or picks another direction.
 */
export type Arrangement = Map<string, XYPosition>;

/**
 * Notes the positions a drag gives nodes. True when a drag ended, which is
 * when there is first something to reset.
 */
export function noteDrags(
  arrangement: Arrangement,
  changes: readonly NodeChange[],
): boolean {
  let ended = false;
  for (const change of changes) {
    // Only a drag carries `dragging`; a position React Flow sets itself does
    // not, and is not the person's.
    if (change.type !== "position" || change.dragging === undefined) continue;
    if (change.position) arrangement.set(change.id, change.position);
    if (!change.dragging) ended = true;
  }
  return ended;
}

/** `nodes`, each one the person dragged at the place they left it. */
export function arranged(
  nodes: Node[],
  arrangement: ReadonlyMap<string, XYPosition>,
): Node[] {
  if (arrangement.size === 0) return nodes;
  return nodes.map((node) => {
    const at = arrangement.get(node.id);
    return at === undefined ||
      (at.x === node.position.x && at.y === node.position.y)
      ? node
      : { ...node, position: at };
  });
}
