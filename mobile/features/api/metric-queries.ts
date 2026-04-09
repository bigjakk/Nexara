import { useQuery } from "@tanstack/react-query";

import { apiGet } from "./api-client";
import { queryKeys } from "./query-keys";
import type { MetricPoint, MetricRange } from "./types";

// Metric history is 5-minute buckets for `1h` range, so polling faster than
// every minute is wasteful. The collector publishes a new point at most once
// per bucket boundary. For per-second live metrics we'd subscribe to the
// `cluster:${id}:metrics` WS channel — deferred to a future enhancement.

export function useVMMetrics(
  clusterId: string | undefined,
  vmId: string | undefined,
  range: MetricRange = "1h",
) {
  return useQuery({
    queryKey: queryKeys.metricHistory("vm", vmId ?? "", range),
    queryFn: () =>
      apiGet<MetricPoint[]>(
        `/clusters/${clusterId ?? ""}/vms/${vmId ?? ""}/metrics?range=${range}`,
      ),
    enabled: Boolean(clusterId && vmId),
    staleTime: 60_000,
    refetchInterval: 60_000,
    refetchIntervalInBackground: false,
  });
}

export function useNodeMetrics(
  clusterId: string | undefined,
  nodeId: string | undefined,
  range: MetricRange = "1h",
) {
  return useQuery({
    queryKey: queryKeys.metricHistory("node", nodeId ?? "", range),
    queryFn: () =>
      apiGet<MetricPoint[]>(
        `/clusters/${clusterId ?? ""}/nodes/${nodeId ?? ""}/metrics?range=${range}`,
      ),
    enabled: Boolean(clusterId && nodeId),
    staleTime: 60_000,
    refetchInterval: 60_000,
    refetchIntervalInBackground: false,
  });
}

export function useClusterMetricsHistory(
  clusterId: string | undefined,
  range: MetricRange = "1h",
) {
  return useQuery({
    queryKey: queryKeys.metricHistory("cluster", clusterId ?? "", range),
    queryFn: () =>
      apiGet<MetricPoint[]>(
        `/clusters/${clusterId ?? ""}/metrics?range=${range}`,
      ),
    enabled: Boolean(clusterId),
    staleTime: 60_000,
    refetchInterval: 60_000,
    refetchIntervalInBackground: false,
  });
}
