import { create } from "zustand";
import type {
  AggregatedMetrics,
  ClusterMetricSummary,
  MetricDataPoint,
  TopConsumer,
} from "@/types/ws";

const MAX_HISTORY_POINTS = 60;
const GBPS_BYTES = 125_000_000; // 1 Gbps in bytes/sec

interface MetricState {
  /** Per-cluster aggregated metrics, keyed by cluster ID. */
  metrics: Map<string, AggregatedMetrics>;
  /** Previous raw snapshot per cluster (for rate calculation). */
  prevSnapshots: Map<
    string,
    { diskRead: number; diskWrite: number; netIn: number; netOut: number; ts: number }
  >;
}

interface MetricActions {
  processMetricMessage: (clusterId: string, payload: ClusterMetricSummary) => void;
  clearCluster: (clusterId: string) => void;
  clearAll: () => void;
}

function computeHealthScore(
  cpuPercent: number,
  memPercent: number,
  netInBps: number,
  netOutBps: number,
  nodeCount: number,
): number {
  const cpuScore = (1 - cpuPercent / 100) * 100;
  const memScore = (1 - memPercent / 100) * 100;
  const storageScore = 100; // Live stream lacks capacity data

  // Network heuristic: compare throughput vs 1 Gbps per node
  const maxBandwidth = nodeCount * GBPS_BYTES;
  const totalNet = netInBps + netOutBps;
  const netUtilization = maxBandwidth > 0 ? totalNet / maxBandwidth : 0;
  const netScore = (1 - Math.min(netUtilization, 1)) * 100;

  const score =
    cpuScore * 0.3 + memScore * 0.3 + storageScore * 0.2 + netScore * 0.2;
  return Math.round(Math.max(0, Math.min(100, score)));
}

function extractTopConsumers(payload: ClusterMetricSummary): TopConsumer[] {
  return payload.vms
    .map((vm) => ({
      vmId: vm.vm_id,
      cpuPercent: vm.cpu_usage * 100,
      memPercent: vm.mem_total > 0 ? (vm.mem_used / vm.mem_total) * 100 : 0,
      memUsed: vm.mem_used,
      memTotal: vm.mem_total,
    }))
    .sort((a, b) => b.cpuPercent - a.cpuPercent)
    .slice(0, 10);
}

export const useMetricStore = create<MetricState & MetricActions>()(
  (set, get) => ({
    metrics: new Map(),
    prevSnapshots: new Map(),

    processMetricMessage: (
      clusterId: string,
      payload: ClusterMetricSummary,
    ) => {
      const state = get();
      const existing = state.metrics.get(clusterId);
      const prev = state.prevSnapshots.get(clusterId);

      // Aggregate node metrics
      let totalMemUsed = 0;
      let totalMemTotal = 0;
      let totalCpu = 0;
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

      // Calculate rates from cumulative counters
      const now = Date.now();
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

      const healthScore = computeHealthScore(
        avgCpu,
        memPercent,
        netInBps,
        netOutBps,
        nodeCount,
      );

      const dataPoint: MetricDataPoint = {
        timestamp: now,
        cpuPercent: avgCpu,
        memPercent,
        diskReadBps,
        diskWriteBps,
        netInBps,
        netOutBps,
      };

      const history = existing ? [...existing.history, dataPoint] : [dataPoint];
      // Cap rolling buffer
      const trimmedHistory =
        history.length > MAX_HISTORY_POINTS
          ? history.slice(history.length - MAX_HISTORY_POINTS)
          : history;

      const topConsumers = extractTopConsumers(payload);

      const aggregated: AggregatedMetrics = {
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
        healthScore,
        history: trimmedHistory,
        topConsumers,
      };

      const newMetrics = new Map(state.metrics);
      newMetrics.set(clusterId, aggregated);

      const newPrev = new Map(state.prevSnapshots);
      newPrev.set(clusterId, {
        diskRead: totalDiskRead,
        diskWrite: totalDiskWrite,
        netIn: totalNetIn,
        netOut: totalNetOut,
        ts: now,
      });

      set({ metrics: newMetrics, prevSnapshots: newPrev });
    },

    clearCluster: (clusterId: string) => {
      const state = get();
      const newMetrics = new Map(state.metrics);
      const newPrev = new Map(state.prevSnapshots);
      newMetrics.delete(clusterId);
      newPrev.delete(clusterId);
      set({ metrics: newMetrics, prevSnapshots: newPrev });
    },

    clearAll: () => {
      set({ metrics: new Map(), prevSnapshots: new Map() });
    },
  }),
);

// Export for testing
export { computeHealthScore, extractTopConsumers, MAX_HISTORY_POINTS };
