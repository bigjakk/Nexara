import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import type {
  NodeResponse,
  VMResponse,
  StorageResponse,
} from "@/types/api";
import type { TopologyInput } from "../lib/topology-transform";

export interface TopologyData {
  input: TopologyInput;
  isLoading: boolean;
  error: Error | null;
}

/**
 * Fetches all topology data: clusters, nodes, VMs, and storage.
 * Reuses existing query keys so WS-driven invalidation works automatically.
 */
export function useTopologyData(): TopologyData {
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];

  const nodeQueries = useQueries({
    queries: clusters.map((cluster) => ({
      queryKey: ["clusters", cluster.id, "nodes"],
      queryFn: () =>
        apiClient.get<NodeResponse[]>(
          `/api/v1/clusters/${cluster.id}/nodes`,
        ),
      enabled: clusters.length > 0,
    })),
  });

  const vmQueries = useQueries({
    queries: clusters.map((cluster) => ({
      queryKey: ["clusters", cluster.id, "vms"],
      queryFn: () =>
        apiClient.get<VMResponse[]>(
          `/api/v1/clusters/${cluster.id}/vms`,
        ),
      enabled: clusters.length > 0,
    })),
  });

  const storageQueries = useQueries({
    queries: clusters.map((cluster) => ({
      queryKey: ["clusters", cluster.id, "storage"],
      queryFn: () =>
        apiClient.get<StorageResponse[]>(
          `/api/v1/clusters/${cluster.id}/storage`,
        ),
      enabled: clusters.length > 0,
    })),
  });

  const isLoading =
    clustersQuery.isLoading ||
    nodeQueries.some((q) => q.isLoading) ||
    vmQueries.some((q) => q.isLoading) ||
    storageQueries.some((q) => q.isLoading);

  const error =
    clustersQuery.error ??
    nodeQueries.find((q) => q.error)?.error ??
    vmQueries.find((q) => q.error)?.error ??
    storageQueries.find((q) => q.error)?.error ??
    null;

  // Keep `input` referentially stable across unrelated re-renders (live WS
  // metric ticks re-render consumers every few seconds). Rebuild only when a
  // query actually delivered fresh data — otherwise the topology graph would
  // re-layout and reset node positions on every metric batch.
  const dataStamp = [
    clustersQuery.dataUpdatedAt,
    ...nodeQueries.map((q) => q.dataUpdatedAt),
    ...vmQueries.map((q) => q.dataUpdatedAt),
    ...storageQueries.map((q) => q.dataUpdatedAt),
  ].join(":");

  const input = useMemo<TopologyInput>(() => {
    const nodesByCluster = new Map<string, NodeResponse[]>();
    const vmsByCluster = new Map<string, VMResponse[]>();
    const storageByCluster = new Map<string, StorageResponse[]>();

    for (let i = 0; i < clusters.length; i++) {
      const cluster = clusters[i];
      if (cluster) {
        nodesByCluster.set(cluster.id, nodeQueries[i]?.data ?? []);
        vmsByCluster.set(cluster.id, vmQueries[i]?.data ?? []);
        storageByCluster.set(cluster.id, storageQueries[i]?.data ?? []);
      }
    }

    return { clusters, nodesByCluster, vmsByCluster, storageByCluster };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataStamp]);

  return { input, isLoading, error };
}
