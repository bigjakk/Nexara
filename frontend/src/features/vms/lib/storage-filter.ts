import type { NodeResponse, StorageResponse } from "@/types/api";

/**
 * filterStorageByContent returns the storages that are usable from the given
 * target node for the given Proxmox content type (e.g. "vztmpl", "rootdir",
 * "images").
 *
 * Why two filters? `storage_pools` in our DB has one row per (cluster, node,
 * storage) tuple. For a shared storage (NFS/CephFS/RBD/etc.) every node owns
 * an identical row; for a per-node-local storage (`local`, `local-lvm`, …)
 * only the owning node has a row.
 *
 * A previous dedupe-by-name implementation surfaced per-node storages from
 * the wrong node, causing the LXC create call to fail at submit time
 * because Proxmox couldn't find the named storage on the target node.
 *
 * Filtering rules:
 *  - Must be active and enabled.
 *  - Must support the requested content type.
 *  - Must be reachable from the target node — either marked shared, or its
 *    `node_id` matches the target node.
 *  - Where the target node has a row of its own for the storage, that row
 *    decides: a shared store disabled or unmounted on that node is left out,
 *    though another node's row for it says it is fine.
 *  - When the target node cannot be resolved (e.g. before the user picks one)
 *    we skip the node-reachability filter to avoid an empty dropdown.
 *  - Results are deduped by storage name and sorted alphabetically.
 */
export function filterStorageByContent(
  storageList: StorageResponse[] | undefined,
  contentType: string,
  nodes: NodeResponse[] | undefined,
  targetNodeName: string,
): StorageResponse[] {
  if (!storageList) return [];

  const targetNodeId =
    targetNodeName && nodes
      ? (nodes.find((n) => n.name === targetNodeName)?.id ?? null)
      : null;

  const ownRow = new Map<string, StorageResponse>();
  if (targetNodeId) {
    for (const s of storageList) {
      if (s.node_id === targetNodeId) ownRow.set(s.storage, s);
    }
  }

  const seen = new Set<string>();
  return storageList
    .filter((s) => {
      // A row must be usable on its own node (its id is how a template list
      // is read), and on the target node, whose own row says so. The first
      // such row does not depend on the target, so a pick keyed by row id
      // survives a node change.
      if (!s.active || !s.enabled) return false;
      const own = ownRow.get(s.storage);
      if (own && (!own.active || !own.enabled)) return false;
      if (
        !s.content
          .split(",")
          .map((c) => c.trim())
          .includes(contentType)
      ) {
        return false;
      }
      // Restrict to storages reachable from the target node. Shared storages
      // are visible on every node; non-shared ones live on one node only.
      if (targetNodeId && !s.shared && s.node_id !== targetNodeId) {
        return false;
      }
      if (seen.has(s.storage)) return false;
      seen.add(s.storage);
      return true;
    })
    .sort((a, b) => a.storage.localeCompare(b.storage));
}
