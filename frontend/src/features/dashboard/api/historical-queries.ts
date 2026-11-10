import { useQuery, useQueries } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type {
  TimeRange,
  HistoricalMetricPoint,
  PressurePoint,
} from "@/types/api";
import type { MetricSeriesPoint } from "@/types/ws";

/**
 * How often a live chart's stored-history seed is refetched. The live buffers
 * hold a limited number of ticks, so a seed fetched once would leave a stretch
 * between its end and the oldest tick kept, with nothing to draw there.
 */
const LIVE_SEED_REFETCH_MS = 60_000;

/**
 * A live chart's seed is also refetched the moment its tab is shown again and
 * the moment its chart is mounted again, however fresh. The interval above
 * runs neither in a hidden tab nor while another page is open, and the ticks
 * kept from before do not reach back over the time away: without this the
 * chart has that stretch until the next interval, a minute on. The app's
 * defaults are not to refetch on focus, and on mount only what is stale.
 */
const LIVE_SEED_ON_RETURN = "always";

export function toSeriesPoints(
  points: HistoricalMetricPoint[],
): MetricSeriesPoint[] {
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
      const data = await apiClient.list<HistoricalMetricPoint>(
        apiPath`/api/v1/clusters/${clusterId}/metrics?range=${range}`,
      );
      return toSeriesPoints(data);
    },
    enabled: range !== "live" && clusterId.length > 0,
    staleTime: 60_000,
  });
}

/**
 * Fetch recent 1h historical data to seed live charts so they aren't empty on
 * load, refetched while mounted so the seed keeps reaching the live ticks.
 */
export function useSeedMetrics(clusterId: string) {
  const query = useQuery({
    queryKey: ["clusters", clusterId, "metrics", "seed"],
    queryFn: async () => {
      const data = await apiClient.list<HistoricalMetricPoint>(
        apiPath`/api/v1/clusters/${clusterId}/metrics?range=1h`,
      );
      return toSeriesPoints(data);
    },
    enabled: clusterId.length > 0,
    staleTime: 5 * 60_000,
    refetchInterval: LIVE_SEED_REFETCH_MS,
    refetchOnWindowFocus: LIVE_SEED_ON_RETURN,
    refetchOnMount: LIVE_SEED_ON_RETURN,
  });
  return query.data;
}

/**
 * Seed data for several clusters at once (shares the per-cluster seed cache).
 * Returns a map of clusterId → seed points; clusters still loading are absent.
 */
export function useSeedMetricsForClusters(
  clusterIds: string[],
): Map<string, MetricSeriesPoint[]> {
  return useQueries({
    queries: clusterIds.map((id) => ({
      queryKey: ["clusters", id, "metrics", "seed"],
      queryFn: async () => {
        const data = await apiClient.list<HistoricalMetricPoint>(
          apiPath`/api/v1/clusters/${id}/metrics?range=1h`,
        );
        return toSeriesPoints(data);
      },
      enabled: id.length > 0,
      staleTime: 5 * 60_000,
      refetchInterval: LIVE_SEED_REFETCH_MS,
      refetchOnWindowFocus: LIVE_SEED_ON_RETURN,
      refetchOnMount: LIVE_SEED_ON_RETURN,
    })),
    combine: (results) => {
      const map = new Map<string, MetricSeriesPoint[]>();
      clusterIds.forEach((id, i) => {
        const data = results[i]?.data;
        if (data) map.set(id, data);
      });
      return map;
    },
  });
}

/**
 * A node's stored history for `range`. With `seedsLive`, it is the seed of a
 * live chart, refetched while mounted so it keeps reaching the live ticks.
 */
export function useNodeHistoricalMetrics(
  clusterId: string,
  nodeId: string,
  range: TimeRange,
  seedsLive = false,
) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeId, "metrics", range],
    queryFn: async () => {
      const data = await apiClient.list<HistoricalMetricPoint>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeId}/metrics?range=${range}`,
      );
      return toSeriesPoints(data);
    },
    enabled: range !== "live" && clusterId.length > 0 && nodeId.length > 0,
    staleTime: 60_000,
    refetchInterval: seedsLive ? LIVE_SEED_REFETCH_MS : false,
    refetchOnWindowFocus: seedsLive ? LIVE_SEED_ON_RETURN : false,
  });
}

/** A guest's stored history for `range`; `seedsLive` as for a node's. */
export function useVMHistoricalMetrics(
  clusterId: string,
  vmId: string,
  range: TimeRange,
  seedsLive = false,
) {
  return useQuery({
    queryKey: ["clusters", clusterId, "vms", vmId, "metrics", range],
    queryFn: async () => {
      const data = await apiClient.list<HistoricalMetricPoint>(
        apiPath`/api/v1/clusters/${clusterId}/vms/${vmId}/metrics?range=${range}`,
      );
      return toSeriesPoints(data);
    },
    enabled: range !== "live" && clusterId.length > 0 && vmId.length > 0,
    staleTime: 60_000,
    refetchInterval: seedsLive ? LIVE_SEED_REFETCH_MS : false,
    refetchOnWindowFocus: seedsLive ? LIVE_SEED_ON_RETURN : false,
  });
}

/** How often a live panel's pressure chart refetches its last hour. */
const LIVE_PRESSURE_REFETCH_MS = 30_000;

/**
 * A node's pressure stall history for `range`; in live mode, the last hour,
 * refetched while the panel is open.
 */
export function useNodePressure(
  clusterId: string,
  nodeId: string,
  range: TimeRange,
) {
  const live = range === "live";
  const span = live ? "1h" : range;
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeId, "pressure", span],
    queryFn: () =>
      apiClient.list<PressurePoint>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeId}/pressure?range=${span}`,
      ),
    enabled: clusterId.length > 0 && nodeId.length > 0,
    staleTime: 60_000,
    refetchInterval: live ? LIVE_PRESSURE_REFETCH_MS : false,
    refetchOnWindowFocus: live ? LIVE_SEED_ON_RETURN : false,
  });
}

/** A guest's pressure stall history for `range`; live as for a node's. */
export function useVMPressure(
  clusterId: string,
  vmId: string,
  range: TimeRange,
) {
  const live = range === "live";
  const span = live ? "1h" : range;
  return useQuery({
    queryKey: ["clusters", clusterId, "vms", vmId, "pressure", span],
    queryFn: () =>
      apiClient.list<PressurePoint>(
        apiPath`/api/v1/clusters/${clusterId}/vms/${vmId}/pressure?range=${span}`,
      ),
    enabled: clusterId.length > 0 && vmId.length > 0,
    staleTime: 60_000,
    refetchInterval: live ? LIVE_PRESSURE_REFETCH_MS : false,
    refetchOnWindowFocus: live ? LIVE_SEED_ON_RETURN : false,
  });
}
