/**
 * Live metric subscription hooks.
 *
 *   - `useClusterLiveMetrics(clusterId)` subscribes the WS channel for one
 *     cluster and returns the aggregated cluster metrics. Mounts on the
 *     cluster detail screen and the dashboard's per-cluster cards.
 *
 *   - `useLiveVM(vmId)` and `useLiveNode(nodeId)` are read-only selectors
 *     that pluck a single resource's live values from the store. They do
 *     NOT subscribe — the calling screen is expected to also call
 *     `useClusterLiveMetrics` for the parent cluster, which is what
 *     drives the actual WS subscription.
 *
 * The store mutates Maps in place and bumps a `version` counter on every
 * update; we read `version` to force re-render and then read the actual
 * value via `getState()` so we always get the freshest data.
 *
 * Channel format and payload shape are defined in
 * `mobile/features/api/metric-ws-types.ts`.
 */

import { useCallback, useEffect, useMemo } from "react";

import type { ClusterMetricSummary } from "@/features/api/metric-ws-types";
import {
  type AggregatedClusterMetrics,
  type LiveResourceMetric,
  useMetricStore,
} from "@/stores/metric-store";
import { useWsStore } from "@/stores/ws-store";

export function useClusterLiveMetrics(
  clusterId: string | undefined,
): AggregatedClusterMetrics | undefined {
  const subscribe = useWsStore((s) => s.subscribe);
  const unsubscribe = useWsStore((s) => s.unsubscribe);
  const processMessage = useMetricStore((s) => s.processMessage);
  // Read version so re-render fires when the store mutates the Maps in place.
  const version = useMetricStore((s) => s.version);

  const handleMessage = useCallback(
    (payload: unknown) => {
      if (!clusterId) return;
      // The backend sends a JSON object that conforms to ClusterMetricSummary.
      // We trust the type because the WS pipeline only forwards messages
      // from the validated Redis channel naming convention; if the payload
      // were malformed, the backend would have rejected it earlier.
      processMessage(clusterId, payload as ClusterMetricSummary);
    },
    [clusterId, processMessage],
  );

  useEffect(() => {
    if (!clusterId) return;
    const channel = `cluster:${clusterId}:metrics`;
    subscribe(channel, handleMessage);
    return () => {
      unsubscribe(channel, handleMessage);
    };
  }, [clusterId, subscribe, unsubscribe, handleMessage]);

  // Read directly via getState so the value is fresh after every version
  // bump. The version selector above triggered the re-render; this read
  // gives us the live data.
  void version;
  if (!clusterId) return undefined;
  return useMetricStore.getState().clusters.get(clusterId);
}

/**
 * Multi-cluster subscription hook for the dashboard. Subscribes to the
 * `cluster:<id>:metrics` channel for every cluster in the input array
 * (one effect with one cleanup, no per-cluster child components needed)
 * and returns a stable Map<clusterId, AggregatedClusterMetrics> rebuilt
 * whenever the metric store version bumps.
 *
 * Pairs with the dashboard's cluster list, where every visible cluster
 * gets a live CPU%/Mem% row. The ws-store's reference counting means
 * navigating from the dashboard into a cluster detail screen (which also
 * subscribes via `useClusterLiveMetrics(id)`) doesn't double-subscribe —
 * the dashboard's subscription is shared.
 */
export function useClustersLiveMetrics(
  clusterIds: string[],
): Map<string, AggregatedClusterMetrics> {
  const subscribe = useWsStore((s) => s.subscribe);
  const unsubscribe = useWsStore((s) => s.unsubscribe);
  const processMessage = useMetricStore((s) => s.processMessage);
  const version = useMetricStore((s) => s.version);

  // Stable dep key — re-running the effect on every render of clusterIds
  // (a fresh array each time) would tear down and rebuild every
  // subscription. Joining the IDs gives us a stable string key.
  const idKey = clusterIds.join(",");

  useEffect(() => {
    if (clusterIds.length === 0) return;
    const handlers = new Map<string, (payload: unknown) => void>();

    for (const id of clusterIds) {
      const channel = `cluster:${id}:metrics`;
      const handler = (payload: unknown) => {
        processMessage(id, payload as ClusterMetricSummary);
      };
      handlers.set(channel, handler);
      subscribe(channel, handler);
    }

    return () => {
      for (const [channel, handler] of handlers) {
        unsubscribe(channel, handler);
      }
    };
    // idKey captures the full set of cluster ids; clusterIds itself is
    // a fresh array each render and would re-trigger needlessly.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [idKey, subscribe, unsubscribe, processMessage]);

  // Read fresh values via getState() and rebuild the result Map only
  // when the store version changes (or the cluster set changes).
  return useMemo(() => {
    const all = useMetricStore.getState().clusters;
    const result = new Map<string, AggregatedClusterMetrics>();
    for (const id of clusterIds) {
      const m = all.get(id);
      if (m) result.set(id, m);
    }
    return result;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [version, idKey]);
}

/**
 * Read-only selector for a single VM's live metrics. Does NOT subscribe —
 * the parent cluster's `useClusterLiveMetrics` must already be mounted on
 * the same (or another) screen for the VM data to be populated.
 */
export function useLiveVM(vmId: string | undefined): LiveResourceMetric | undefined {
  const version = useMetricStore((s) => s.version);
  void version;
  if (!vmId) return undefined;
  return useMetricStore.getState().vms.get(vmId);
}

/**
 * Read-only selector for a single node's live metrics. Same caveats as
 * `useLiveVM` — the cluster subscription must be mounted upstream.
 */
export function useLiveNode(
  nodeId: string | undefined,
): LiveResourceMetric | undefined {
  const version = useMetricStore((s) => s.version);
  void version;
  if (!nodeId) return undefined;
  return useMetricStore.getState().nodes.get(nodeId);
}
