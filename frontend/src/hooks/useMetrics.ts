import { useEffect, useCallback } from "react";
import { useWebSocketStore } from "@/stores/websocket-store";
import { useMetricStore } from "@/stores/metric-store";
import type { AggregatedMetrics, ClusterMetricSummary } from "@/types/ws";

/**
 * Subscribe to live metrics for a single cluster.
 * Returns the aggregated metrics from the metric store.
 */
export function useClusterMetrics(
  clusterId: string,
): AggregatedMetrics | undefined {
  const subscribe = useWebSocketStore((s) => s.subscribe);
  const unsubscribe = useWebSocketStore((s) => s.unsubscribe);
  const processMetricMessage = useMetricStore((s) => s.processMetricMessage);
  const metrics = useMetricStore((s) => s.metrics.get(clusterId));

  const handleMessage = useCallback(
    (payload: unknown) => {
      processMetricMessage(
        clusterId,
        payload as ClusterMetricSummary,
      );
    },
    [clusterId, processMetricMessage],
  );

  useEffect(() => {
    const channel = `cluster:${clusterId}:metrics`;
    subscribe(channel, handleMessage);
    return () => {
      unsubscribe(channel, handleMessage);
    };
  }, [clusterId, subscribe, unsubscribe, handleMessage]);

  return metrics;
}

/**
 * Subscribe to live metrics for multiple clusters.
 * Returns a Map of clusterId -> AggregatedMetrics.
 */
export function useDashboardMetrics(
  clusterIds: string[],
): Map<string, AggregatedMetrics> {
  const subscribe = useWebSocketStore((s) => s.subscribe);
  const unsubscribe = useWebSocketStore((s) => s.unsubscribe);
  const processMetricMessage = useMetricStore((s) => s.processMetricMessage);
  const allMetrics = useMetricStore((s) => s.metrics);

  useEffect(() => {
    const handlers = new Map<string, (payload: unknown) => void>();

    for (const id of clusterIds) {
      const channel = `cluster:${id}:metrics`;
      const handler = (payload: unknown) => {
        processMetricMessage(id, payload as ClusterMetricSummary);
      };
      handlers.set(channel, handler);
      subscribe(channel, handler);
    }

    return () => {
      for (const [channel, handler] of handlers) {
        unsubscribe(channel, handler);
      }
    };
  }, [clusterIds, subscribe, unsubscribe, processMetricMessage]);

  // Filter to only the requested cluster IDs
  const result = new Map<string, AggregatedMetrics>();
  for (const id of clusterIds) {
    const m = allMetrics.get(id);
    if (m) {
      result.set(id, m);
    }
  }
  return result;
}
