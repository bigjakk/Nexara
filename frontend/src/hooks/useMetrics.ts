import { useEffect, useCallback, useRef, useMemo } from "react";
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
 * Returns a stable Map reference that only updates when metric data actually changes.
 */
export function useDashboardMetrics(
  clusterIds: string[],
): Map<string, AggregatedMetrics> {
  const subscribe = useWebSocketStore((s) => s.subscribe);
  const unsubscribe = useWebSocketStore((s) => s.unsubscribe);
  const processMetricMessage = useMetricStore((s) => s.processMetricMessage);

  // Track the last version counter to know when to rebuild the result map
  const version = useMetricStore((s) => s.version);
  const resultRef = useRef(new Map<string, AggregatedMetrics>());

  // Stable cluster ID list reference
  const clusterIdKey = clusterIds.join(",");

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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterIdKey, subscribe, unsubscribe, processMetricMessage]);

  // Only rebuild the result map when the store version changes
  return useMemo(() => {
    const allMetrics = useMetricStore.getState().metrics;
    const result = new Map<string, AggregatedMetrics>();
    for (const id of clusterIds) {
      const m = allMetrics.get(id);
      if (m) {
        result.set(id, m);
      }
    }
    resultRef.current = result;
    return result;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [version, clusterIdKey]);
}
