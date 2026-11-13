import type { TopologyInput } from "./topology-transform";

export interface TopologyCounts {
  clusters: number;
  nodes: number;
  nodesOffline: number;
  guests: number;
  /** Storage as the map draws it: a shared store once per cluster. */
  storage: number;
}

/**
 * What the map holds, for the sentence under the page title. Counted for the
 * one cluster the map is filtered to, or for all of them.
 */
export function topologyCounts(
  input: TopologyInput,
  selectedClusterId: string | null,
): TopologyCounts {
  const counts: TopologyCounts = {
    clusters: 0,
    nodes: 0,
    nodesOffline: 0,
    guests: 0,
    storage: 0,
  };
  for (const cluster of input.clusters) {
    if (selectedClusterId !== null && cluster.id !== selectedClusterId) {
      continue;
    }
    counts.clusters += 1;
    const nodes = input.nodesByCluster.get(cluster.id) ?? [];
    counts.nodes += nodes.length;
    counts.nodesOffline += nodes.filter((n) => n.status === "offline").length;
    // A template is not a guest anyone runs.
    counts.guests += (input.vmsByCluster.get(cluster.id) ?? []).filter(
      (vm) => !vm.template,
    ).length;
    const shared = new Set<string>();
    for (const store of input.storageByCluster.get(cluster.id) ?? []) {
      if (!store.shared) counts.storage += 1;
      else shared.add(store.storage);
    }
    counts.storage += shared.size;
  }
  return counts;
}
