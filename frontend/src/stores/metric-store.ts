import { create } from "zustand";
import type {
  AggregatedMetrics,
  ClusterMetricSummary,
  MetricDataPoint,
  TopConsumer,
  VmLiveMetric,
} from "@/types/ws";

const MAX_HISTORY_POINTS = 60;
const DEFAULT_REFRESH_INTERVAL = 10_000;
const REFRESH_INTERVAL_KEY = "nexara:refreshInterval";

function loadRefreshInterval(): number {
  try {
    const stored = localStorage.getItem(REFRESH_INTERVAL_KEY);
    if (stored !== null) {
      const val = Number(stored);
      if (!Number.isNaN(val) && val > 0) return val;
    }
  } catch {
    // localStorage unavailable (SSR, private mode, etc.)
  }
  return DEFAULT_REFRESH_INTERVAL;
}

interface MetricState {
  /** Per-cluster aggregated metrics, keyed by cluster ID. */
  metrics: Map<string, AggregatedMetrics>;
  /** Monotonic counter incremented on each store update. */
  version: number;
  /** How often (ms) the UI should process incoming metric messages. */
  refreshInterval: number;
  /** Timestamp of last processed message per cluster. */
  lastProcessed: Map<string, number>;
}

interface MetricActions {
  processMetricMessage: (clusterId: string, payload: ClusterMetricSummary) => void;
  setRefreshInterval: (ms: number) => void;
  clearCluster: (clusterId: string) => void;
  clearAll: () => void;
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
    version: 0,
    refreshInterval: loadRefreshInterval(),
    lastProcessed: new Map(),

    processMetricMessage: (
      clusterId: string,
      payload: ClusterMetricSummary,
    ) => {
      const state = get();

      // Throttle: skip if we processed this cluster too recently. The server
      // now ships rates pre-computed, so a skipped frame is just dropped — we
      // do not need to track any state to keep the next frame accurate.
      const now = Date.now();
      const lastTs = state.lastProcessed.get(clusterId);
      if (lastTs !== undefined && now - lastTs < state.refreshInterval - 500) {
        return;
      }
      state.lastProcessed.set(clusterId, now);

      const existing = state.metrics.get(clusterId);

      // Aggregate node metrics
      let totalMemUsed = 0;
      let totalMemTotal = 0;
      let totalCpu = 0;
      let diskReadBps = 0;
      let diskWriteBps = 0;
      let netInBps = 0;
      let netOutBps = 0;

      for (const node of payload.nodes) {
        totalCpu += node.cpu_usage;
        totalMemUsed += node.mem_used;
        totalMemTotal += node.mem_total;
        diskReadBps += node.disk_read_bps;
        diskWriteBps += node.disk_write_bps;
        netInBps += node.net_in_bps;
        netOutBps += node.net_out_bps;
      }

      const nodeCount = payload.nodes.length;
      const avgCpu = nodeCount > 0 ? (totalCpu / nodeCount) * 100 : 0;
      const memPercent =
        totalMemTotal > 0 ? (totalMemUsed / totalMemTotal) * 100 : 0;

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

      const vmMetrics = new Map<string, VmLiveMetric>();
      for (const vm of payload.vms) {
        vmMetrics.set(vm.vm_id, {
          cpuPercent: vm.cpu_usage * 100,
          memPercent: vm.mem_total > 0 ? (vm.mem_used / vm.mem_total) * 100 : 0,
          diskReadBps: vm.disk_read_bps,
          diskWriteBps: vm.disk_write_bps,
          netInBps: vm.net_in_bps,
          netOutBps: vm.net_out_bps,
        });
      }

      const nodeMetrics = new Map<string, VmLiveMetric>();
      for (const nd of payload.nodes) {
        nodeMetrics.set(nd.node_id, {
          cpuPercent: nd.cpu_usage * 100,
          memPercent: nd.mem_total > 0 ? (nd.mem_used / nd.mem_total) * 100 : 0,
          diskReadBps: nd.disk_read_bps,
          diskWriteBps: nd.disk_write_bps,
          netInBps: nd.net_in_bps,
          netOutBps: nd.net_out_bps,
        });
      }

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
        history: trimmedHistory,
        topConsumers,
        vmMetrics,
        nodeMetrics,
      };

      state.metrics.set(clusterId, aggregated);
      set({ version: state.version + 1 });
    },

    setRefreshInterval: (ms: number) => {
      try {
        localStorage.setItem(REFRESH_INTERVAL_KEY, String(ms));
      } catch {
        // localStorage unavailable
      }
      set({ refreshInterval: ms });
    },

    clearCluster: (clusterId: string) => {
      const state = get();
      state.metrics.delete(clusterId);
      set({ version: state.version + 1 });
    },

    clearAll: () => {
      set({
        metrics: new Map(),
        version: 0,
        lastProcessed: new Map(),
      });
    },
  }),
);

// Export for testing
export { extractTopConsumers, MAX_HISTORY_POINTS, DEFAULT_REFRESH_INTERVAL };
