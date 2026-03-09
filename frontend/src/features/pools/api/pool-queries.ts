import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

export interface ResourcePool {
  poolid: string;
  comment?: string;
  [key: string]: unknown;
}

export interface PoolMember {
  id: string;
  node: string;
  type: string;
  vmid?: number;
  name?: string;
  storage?: string;
  status?: string;
  [key: string]: unknown;
}

export interface ResourcePoolDetail {
  poolid: string;
  comment?: string;
  members?: PoolMember[];
  [key: string]: unknown;
}

export function useResourcePools(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "pools"],
    queryFn: () => apiClient.get<ResourcePool[]>(`/api/v1/clusters/${clusterId}/pools`),
    enabled: clusterId.length > 0,
  });
}

export function useResourcePool(clusterId: string, poolId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "pools", poolId],
    queryFn: () => apiClient.get<ResourcePoolDetail>(`/api/v1/clusters/${clusterId}/pools/${encodeURIComponent(poolId)}`),
    enabled: clusterId.length > 0 && poolId.length > 0,
  });
}

export function useCreatePool(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { poolid: string; comment?: string }) =>
      apiClient.post(`/api/v1/clusters/${clusterId}/pools`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "pools"] }); },
  });
}

export function useUpdatePool(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ poolid, ...data }: { poolid: string; comment?: string; vms?: string; storage?: string; delete?: string }) =>
      apiClient.put(`/api/v1/clusters/${clusterId}/pools/${encodeURIComponent(poolid)}`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "pools"] }); },
  });
}

export function useDeletePool(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (poolId: string) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/pools/${encodeURIComponent(poolId)}`),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "pools"] }); },
  });
}
