import type { VMFolder } from "@/types/api";

/** Route segment used by the folder detail page for the "Discovered"
 * pseudo-folder (VMs with no folder assignment). Never collides with a real
 * folder id — those are UUIDs. */
export const UNASSIGNED_FOLDER_SEGMENT = "unassigned";

export interface FolderNode {
  folder: VMFolder;
  children: FolderNode[];
}

/** Build a forest of folder nodes from a flat list. Orphans (parent_id
 * pointing at a folder we don't have) bubble up to the root. */
export function buildFolderTree(folders: VMFolder[]): FolderNode[] {
  const byId = new Map<string, FolderNode>();
  for (const f of folders) {
    byId.set(f.id, { folder: f, children: [] });
  }
  const roots: FolderNode[] = [];
  for (const node of byId.values()) {
    const parentId = node.folder.parent_id;
    const parent = parentId ? byId.get(parentId) : undefined;
    if (parent) {
      parent.children.push(node);
    } else {
      roots.push(node);
    }
  }
  // Sort each level alphabetically.
  const sortRec = (nodes: FolderNode[]) => {
    nodes.sort((a, b) => a.folder.name.localeCompare(b.folder.name));
    for (const n of nodes) sortRec(n.children);
  };
  sortRec(roots);
  return roots;
}

/** Collect a folder's own id plus the ids of every descendant folder.
 * Cycle-safe (a corrupt parent chain can't loop) and tolerant of a rootId
 * that isn't in the list (returns just the root id). */
export function collectSubtreeFolderIds(
  folders: VMFolder[],
  rootId: string,
): Set<string> {
  const childrenOf = new Map<string, string[]>();
  for (const f of folders) {
    if (f.parent_id !== null) {
      const list = childrenOf.get(f.parent_id) ?? [];
      list.push(f.id);
      childrenOf.set(f.parent_id, list);
    }
  }
  const out = new Set<string>();
  const stack = [rootId];
  while (stack.length > 0) {
    const id = stack.pop();
    if (id === undefined || out.has(id)) continue;
    out.add(id);
    for (const child of childrenOf.get(id) ?? []) stack.push(child);
  }
  return out;
}

export interface FlatFolderEntry {
  folder: VMFolder;
  depth: number;
}

/** Walk the folder forest in depth-first order, returning each folder
 * paired with its depth. Used by pickers/lists that want one row per
 * folder with indentation. */
export function flattenFolderTree(tree: FolderNode[]): FlatFolderEntry[] {
  const out: FlatFolderEntry[] = [];
  const walk = (nodes: FolderNode[], depth: number) => {
    for (const n of nodes) {
      out.push({ folder: n.folder, depth });
      walk(n.children, depth + 1);
    }
  };
  walk(tree, 0);
  return out;
}
