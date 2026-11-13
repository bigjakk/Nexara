/**
 * The dashboard header's counts: what the estate holds and how much of it is
 * up. Counted as the Fleet widget counts, so the two never disagree.
 */
import type { VMResponse } from "@/types/api";
import type { ClusterSummary } from "../api/dashboard-queries";

export interface EstateCounts {
  clusters: number;
  nodes: number;
  nodesOffline: number;
  guests: number;
  guestsRunning: number;
}

export interface GuestCounts {
  vms: number;
  vmsRunning: number;
  containers: number;
  containersRunning: number;
}

/**
 * The guests in one or more clusters' lists, by kind, under the same rule:
 * a template is not a guest anyone runs.
 */
export function guestCounts(
  lists: Iterable<readonly VMResponse[]>,
): GuestCounts {
  const counts: GuestCounts = {
    vms: 0,
    vmsRunning: 0,
    containers: 0,
    containersRunning: 0,
  };
  for (const list of lists) {
    for (const vm of list) {
      if (vm.template) continue;
      const running = vm.status === "running" ? 1 : 0;
      if (vm.type === "lxc") {
        counts.containers += 1;
        counts.containersRunning += running;
      } else {
        counts.vms += 1;
        counts.vmsRunning += running;
      }
    }
  }
  return counts;
}

export function estateCounts(
  summaries: readonly ClusterSummary[],
  vmsByCluster: ReadonlyMap<string, VMResponse[]> | undefined,
): EstateCounts {
  const counts: EstateCounts = {
    clusters: summaries.length,
    nodes: 0,
    nodesOffline: 0,
    guests: 0,
    guestsRunning: 0,
  };
  for (const summary of summaries) {
    counts.nodes += summary.nodeCount;
    counts.nodesOffline += summary.nodeCount - summary.nodesOnline;
    // A template is not a guest anyone runs.
    for (const vm of vmsByCluster?.get(summary.cluster.id) ?? []) {
      if (vm.template) continue;
      counts.guests += 1;
      if (vm.status === "running") counts.guestsRunning += 1;
    }
  }
  return counts;
}
