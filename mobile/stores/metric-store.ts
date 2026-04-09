/**
 * Live metric store. Subscribes nothing itself — `useClusterLiveMetrics`
 * (or any future hook) drives WS subscription and pushes incoming
 * `ClusterMetricSummary` payloads through `processMessage()`. This store
 * is just the state container and the rate-calculation engine.
 *
 * Mirrors `frontend/src/stores/metric-store.ts` but slimmed down for
 * mobile use cases:
 *   - No 60-point rolling history buffer (mobile sparklines use the
 *     REST metric history endpoint, not live WS data)
 *   - No top-10-VMs computation (the dashboard tab on mobile doesn't
 *     show top consumers — could be added later if needed)
 *   - No refresh-interval throttle (we let every message through; the
 *     collector already runs on a 10-15s interval per the backend
 *     scheduler)
 *   - Per-VM and per-node CPU/Mem percent maps so individual detail
 *     screens can pluck their resource's live values without iterating
 *     the whole array
 *
 * The `version` counter increments on every successful update so consumers
 * can use a Zustand selector that depends on `version` to force a re-render
 * when the underlying Maps mutate (Zustand can't detect Map mutations
 * shallowly).
 *
 * Cleared on logout via the auth store's bootstrap reset path.
 */

import { create } from "zustand";

import type { ClusterMetricSummary } from "@/features/api/metric-ws-types";

/**
 * Live values for a single VM or node. Percentages are 0-100, not 0-1.
 */
export interface LiveResourceMetric {
  cpuPercent: number;
  memPercent: number;
  memUsed: number;
  memTotal: number;
}

/**
 * Aggregated cluster state derived from the latest snapshot.
 *
 * `diskReadBps` / `netInBps` etc. are calculated by diffing the cumulative
 * counters in the current payload against the previous snapshot for this
 * cluster. They are zero on the first message (no previous snapshot to
 * diff against).
 */
export interface AggregatedClusterMetrics {
  cpuPercent: number;
  memPercent: number;
  memUsed: number;
  memTotal: number;
  diskReadBps: number;
  diskWriteBps: number;
  netInBps: number;
  netOutBps: number;
  nodeCount: number;
  vmCount: number;
  /** Last update timestamp (ms since epoch) */
  updatedAt: number;
}

interface PrevSnapshot {
  diskRead: number;
  diskWrite: number;
  netIn: number;
  netOut: number;
  ts: number;
}

interface MetricState {
  /** Per-cluster aggregated values, keyed by cluster ID. */
  clusters: Map<string, AggregatedClusterMetrics>;
  /** Per-VM live values, keyed by VM row UUID. */
  vms: Map<string, LiveResourceMetric>;
  /** Per-node live values, keyed by node row UUID. */
  nodes: Map<string, LiveResourceMetric>;
  /** Per-cluster previous snapshot, used to compute BPS rates. */
  prevSnapshots: Map<string, PrevSnapshot>;
  /**
   * `collected_at` of the most recently processed payload per cluster.
   * Used to dedupe — if multiple screens subscribe to the same cluster
   * channel (e.g. cluster detail + VM detail), the same payload would
   * otherwise be processed once per listener. Two consecutive processings
   * within the same event loop tick zero out the BPS rate calculation
   * because `dtSec` becomes ~0; deduping by collected_at avoids that
   * entirely and is also a small CPU win.
   */
  lastCollectedAt: Map<string, string>;
  /** Monotonic counter incremented on every store update. */
  version: number;
}

interface MetricActions {
  processMessage: (clusterId: string, payload: ClusterMetricSummary) => void;
  clearCluster: (clusterId: string) => void;
  clearAll: () => void;
}

export const useMetricStore = create<MetricState & MetricActions>((set, get) => ({
  clusters: new Map(),
  vms: new Map(),
  nodes: new Map(),
  prevSnapshots: new Map(),
  lastCollectedAt: new Map(),
  version: 0,

  processMessage: (clusterId, payload) => {
    const state = get();

    // Dedupe — if we've already processed a payload with this collected_at
    // for this cluster, skip. Prevents the rate calculation from collapsing
    // to zero when multiple screens subscribe to the same channel.
    const lastSeen = state.lastCollectedAt.get(clusterId);
    if (lastSeen !== undefined && lastSeen === payload.collected_at) {
      return;
    }
    state.lastCollectedAt.set(clusterId, payload.collected_at);

    const now = Date.now();
    const prev = state.prevSnapshots.get(clusterId);

    // Aggregate cluster-wide totals across nodes.
    let totalCpu = 0;
    let totalMemUsed = 0;
    let totalMemTotal = 0;
    let totalDiskRead = 0;
    let totalDiskWrite = 0;
    let totalNetIn = 0;
    let totalNetOut = 0;

    for (const node of payload.nodes) {
      totalCpu += node.cpu_usage;
      totalMemUsed += node.mem_used;
      totalMemTotal += node.mem_total;
      totalDiskRead += node.disk_read;
      totalDiskWrite += node.disk_write;
      totalNetIn += node.net_in;
      totalNetOut += node.net_out;
    }

    const nodeCount = payload.nodes.length;
    const avgCpu = nodeCount > 0 ? (totalCpu / nodeCount) * 100 : 0;
    const memPercent =
      totalMemTotal > 0 ? (totalMemUsed / totalMemTotal) * 100 : 0;

    // Rate calculation — diff cumulative counters against prev snapshot.
    let diskReadBps = 0;
    let diskWriteBps = 0;
    let netInBps = 0;
    let netOutBps = 0;
    if (prev) {
      const dtSec = (now - prev.ts) / 1000;
      if (dtSec > 0) {
        diskReadBps = Math.max(0, (totalDiskRead - prev.diskRead) / dtSec);
        diskWriteBps = Math.max(
          0,
          (totalDiskWrite - prev.diskWrite) / dtSec,
        );
        netInBps = Math.max(0, (totalNetIn - prev.netIn) / dtSec);
        netOutBps = Math.max(0, (totalNetOut - prev.netOut) / dtSec);
      }
    }

    state.clusters.set(clusterId, {
      cpuPercent: avgCpu,
      memPercent,
      memUsed: totalMemUsed,
      memTotal: totalMemTotal,
      diskReadBps,
      diskWriteBps,
      netInBps,
      netOutBps,
      nodeCount,
      vmCount: payload.vm_count,
      updatedAt: now,
    });

    state.prevSnapshots.set(clusterId, {
      diskRead: totalDiskRead,
      diskWrite: totalDiskWrite,
      netIn: totalNetIn,
      netOut: totalNetOut,
      ts: now,
    });

    // Per-node lookup — overwrites any previous values for these nodes.
    for (const node of payload.nodes) {
      state.nodes.set(node.node_id, {
        cpuPercent: node.cpu_usage * 100,
        memPercent:
          node.mem_total > 0 ? (node.mem_used / node.mem_total) * 100 : 0,
        memUsed: node.mem_used,
        memTotal: node.mem_total,
      });
    }

    // Per-VM lookup.
    for (const vm of payload.vms) {
      state.vms.set(vm.vm_id, {
        cpuPercent: vm.cpu_usage * 100,
        memPercent: vm.mem_total > 0 ? (vm.mem_used / vm.mem_total) * 100 : 0,
        memUsed: vm.mem_used,
        memTotal: vm.mem_total,
      });
    }

    set({ version: state.version + 1 });
  },

  clearCluster: (clusterId) => {
    const state = get();
    state.clusters.delete(clusterId);
    state.prevSnapshots.delete(clusterId);
    state.lastCollectedAt.delete(clusterId);
    set({ version: state.version + 1 });
  },

  clearAll: () => {
    set({
      clusters: new Map(),
      vms: new Map(),
      nodes: new Map(),
      prevSnapshots: new Map(),
      lastCollectedAt: new Map(),
      version: 0,
    });
  },
}));
