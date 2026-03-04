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
