import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { ClusterResponse, NodeResponse } from "@/types/api";

export function useCluster(id: string) {
  return useQuery({
    queryKey: ["clusters", id],
    queryFn: () => apiClient.get<ClusterResponse>(`/api/v1/clusters/${id}`),
    enabled: id.length > 0,
  });
}

export function useClusterNodes(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes"],
    queryFn: () =>
      apiClient.get<NodeResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes`,
      ),
    enabled: clusterId.length > 0,
  });
}
