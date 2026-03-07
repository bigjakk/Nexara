import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { TimeRange, HistoricalMetricPoint } from "@/types/api";
import type { MetricDataPoint } from "@/types/ws";

export function toMetricDataPoints(
  points: HistoricalMetricPoint[],
): MetricDataPoint[] {
  return points.map((p) => ({
    timestamp: p.timestamp,
    cpuPercent: p.cpuPercent,
    memPercent: p.memPercent,
    diskReadBps: p.diskReadBps,
    diskWriteBps: p.diskWriteBps,
    netInBps: p.netInBps,
    netOutBps: p.netOutBps,
  }));
}

export function useHistoricalMetrics(clusterId: string, range: TimeRange) {
  return useQuery({
    queryKey: ["clusters", clusterId, "metrics", range],
    queryFn: async () => {
      const data = await apiClient.get<HistoricalMetricPoint[]>(
        `/api/v1/clusters/${clusterId}/metrics?range=${range}`,
      );
      return toMetricDataPoints(data);
    },
    enabled: range !== "live" && clusterId.length > 0,
    staleTime: 60_000,
  });
}

/**
 * Fetch recent 1h historical data to seed live charts so they aren't empty on load.
 * This runs once and caches — live WS data takes over once it starts arriving.
 */
export function useSeedMetrics(clusterId: string) {
  const query = useQuery({
    queryKey: ["clusters", clusterId, "metrics", "seed"],
    queryFn: async () => {
      const data = await apiClient.get<HistoricalMetricPoint[]>(
        `/api/v1/clusters/${clusterId}/metrics?range=1h`,
      );
      return toMetricDataPoints(data);
    },
    enabled: clusterId.length > 0,
    staleTime: 5 * 60_000,
  });
  return query.data;
}

export function useNodeHistoricalMetrics(clusterId: string, nodeId: string, range: TimeRange) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeId, "metrics", range],
    queryFn: async () => {
      const data = await apiClient.get<HistoricalMetricPoint[]>(
        `/api/v1/clusters/${clusterId}/nodes/${nodeId}/metrics?range=${range}`,
      );
      return toMetricDataPoints(data);
    },
    enabled: range !== "live" && clusterId.length > 0 && nodeId.length > 0,
    staleTime: 60_000,
  });
}

export function useVMHistoricalMetrics(clusterId: string, vmId: string, range: TimeRange) {
  return useQuery({
    queryKey: ["clusters", clusterId, "vms", vmId, "metrics", range],
    queryFn: async () => {
      const data = await apiClient.get<HistoricalMetricPoint[]>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/metrics?range=${range}`,
      );
      return toMetricDataPoints(data);
    },
    enabled: range !== "live" && clusterId.length > 0 && vmId.length > 0,
    staleTime: 60_000,
  });
}
