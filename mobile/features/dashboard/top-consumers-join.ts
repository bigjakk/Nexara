/**
 * Pure data join for the TopConsumers dashboard widget. Extracted from
 * `TopConsumers.tsx` so the join logic can be tested without mounting
 * a React tree (TanStack Query, Zustand store, expo-router, etc.).
 *
 * The join takes three inputs:
 *   1. The list of clusters the user can see
 *   2. A parallel array of fetched VM lists (one per cluster, may be
 *      undefined if that cluster's query is still in flight)
 *   3. A Map of live per-VM metrics from the metric store
 *
 * It produces the top N VMs by CPU%, with each row carrying the joined
 * data needed to render and to navigate to the VM detail screen.
 *
 * Filters:
 *   - VM must exist in BOTH the cluster VM list AND the live metric Map
 *   - Templates are excluded (defense-in-depth — Proxmox doesn't push
 *     metrics for templates anyway)
 */

import type { Cluster, VM } from "@/features/api/types";
import type { LiveResourceMetric } from "@/stores/metric-store";

export interface TopConsumerRow {
  vm: VM;
  clusterId: string;
  clusterName: string;
  cpuPercent: number;
  memPercent: number;
}

export const DEFAULT_MAX_ROWS = 10;

/**
 * Build the top-consumers list from the joined inputs. Pure function,
 * no React, no Zustand, no Tanstack Query.
 *
 * @param clusters list of clusters the user can see
 * @param vmsByCluster parallel array — `vmsByCluster[i]` is the VM list
 *   for `clusters[i]`, or undefined if not yet loaded
 * @param liveVMs map keyed by VM row UUID → live CPU/Mem values
 * @param maxRows defaults to 10
 */
export function joinTopConsumers(
  clusters: readonly Cluster[],
  vmsByCluster: readonly (readonly VM[] | undefined)[],
  liveVMs: ReadonlyMap<string, LiveResourceMetric>,
  maxRows: number = DEFAULT_MAX_ROWS,
): TopConsumerRow[] {
  // Build vmId → { vm, clusterId, clusterName } lookup
  const vmIndex = new Map<
    string,
    { vm: VM; clusterId: string; clusterName: string }
  >();
  for (let i = 0; i < clusters.length; i++) {
    const cluster = clusters[i];
    const data = vmsByCluster[i];
    if (!cluster || !data) continue;
    for (const vm of data) {
      vmIndex.set(vm.id, {
        vm,
        clusterId: cluster.id,
        clusterName: cluster.name,
      });
    }
  }

  // Walk the live VM map and emit rows for VMs we can resolve
  const rows: TopConsumerRow[] = [];
  for (const [vmId, live] of liveVMs) {
    const idx = vmIndex.get(vmId);
    if (!idx) continue;
    if (idx.vm.template) continue;
    rows.push({
      vm: idx.vm,
      clusterId: idx.clusterId,
      clusterName: idx.clusterName,
      cpuPercent: live.cpuPercent,
      memPercent: live.memPercent,
    });
  }

  rows.sort((a, b) => b.cpuPercent - a.cpuPercent);
  return rows.slice(0, maxRows);
}
