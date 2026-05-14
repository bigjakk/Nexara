import type { VMFolder } from "@/types/api";

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
