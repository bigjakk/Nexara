import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type { AptRepositoryResponse } from "@/types/api";

export function useNodeAptRepositories(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "apt-repositories"],
    queryFn: () =>
      apiClient.get<AptRepositoryResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/apt/repositories`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useToggleAptRepository(clusterId: string, nodeName: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      path: string;
      index: number;
      enabled: boolean;
      digest: string;
    }) =>
      apiClient.put<{ status: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/apt/repositories`,
        data,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: [
          "clusters",
          clusterId,
          "nodes",
          nodeName,
          "apt-repositories",
        ],
      });
    },
  });
}

export function useAddStandardAptRepository(
  clusterId: string,
  nodeName: string,
) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { handle: string; digest: string }) =>
      apiClient.post<{ status: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/apt/repositories`,
        data,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: [
          "clusters",
          clusterId,
          "nodes",
          nodeName,
          "apt-repositories",
        ],
      });
    },
  });
}
