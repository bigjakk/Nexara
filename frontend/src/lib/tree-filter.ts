/** One row of a tree, as the filter box sees it. */
export interface TreeFilterNode {
  /** Unique within the tree it is filtered with. */
  id: string;
  /** What the row shows. Matched as a substring, without regard to case. */
  label: string;
  children?: readonly TreeFilterNode[];
}

export interface TreeFilterResult {
  /** Rows to keep: a match, everything above one, and everything under one. */
  visible: ReadonlySet<string>;
  /**
   * Branches to hold open so a match under them can be seen. Held, not
   * stored: the tree's own expansion is left alone, and is what shows again
   * once the box is cleared.
   */
  open: ReadonlySet<string>;
}

/** What the box holds, as the rows are compared with it. */
export function normalizeTreeFilter(raw: string): string {
  return raw.trim().toLowerCase();
}

/**
 * Narrows a tree to the rows whose label contains `query` (already
 * normalised). Null for a blank query: nothing is filtered, and callers skip
 * the lookups.
 */
export function filterTree(
  roots: readonly TreeFilterNode[],
  query: string,
): TreeFilterResult | null {
  if (query === "") return null;
  const visible = new Set<string>();
  const open = new Set<string>();

  /** Whether `node` or anything under it matches by itself. */
  function visit(node: TreeFilterNode, underMatch: boolean): boolean {
    const matches = node.label.toLowerCase().includes(query);
    let matchBelow = false;
    for (const child of node.children ?? []) {
      if (visit(child, underMatch || matches)) matchBelow = true;
    }
    if (matchBelow) open.add(node.id);
    if (underMatch || matches || matchBelow) visible.add(node.id);
    return matches || matchBelow;
  }

  for (const root of roots) visit(root, false);
  return { visible, open };
}
