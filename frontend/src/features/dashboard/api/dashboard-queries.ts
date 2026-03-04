import { useQuery, useQueries } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  ClusterResponse,
  NodeResponse,
  VMResponse,
  StorageResponse,
} from "@/types/api";

export function useClusters() {
  return useQuery({
    queryKey: ["clusters"],
    queryFn: () => apiClient.get<ClusterResponse[]>("/api/v1/clusters"),
  });
}

export interface ClusterSummary {
  cluster: ClusterResponse;
  nodeCount: number;
  vmCount: number;
  containerCount: number;
  storageTotalBytes: number;
}

export interface DashboardData {
  clusters: ClusterSummary[];
  totalNodes: number;
  totalVMs: number;
  totalContainers: number;
  totalStorageBytes: number;
}

export function useDashboardData() {
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
        apiClient.get<VMResponse[]>(`/api/v1/clusters/${cluster.id}/vms`),
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

  let data: DashboardData | undefined;

  if (!isLoading && !error && clustersQuery.data) {
    let totalNodes = 0;
    let totalVMs = 0;
    let totalContainers = 0;
    let totalStorageBytes = 0;

    const clusterSummaries = clusters.map((cluster, i) => {
      const nodes = nodeQueries[i]?.data ?? [];
      const vms = vmQueries[i]?.data ?? [];
      const storage = storageQueries[i]?.data ?? [];

      const vmCount = vms.filter((v) => v.type === "qemu").length;
      const containerCount = vms.filter((v) => v.type === "lxc").length;
      const storageTotalBytes = storage.reduce((sum, s) => sum + s.total, 0);

      totalNodes += nodes.length;
      totalVMs += vmCount;
      totalContainers += containerCount;
      totalStorageBytes += storageTotalBytes;

      return {
        cluster,
        nodeCount: nodes.length,
        vmCount,
        containerCount,
        storageTotalBytes,
      };
    });

    data = {
      clusters: clusterSummaries,
      totalNodes,
      totalVMs,
      totalContainers,
      totalStorageBytes,
    };
  }

  return { data, isLoading, error };
}
