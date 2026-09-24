import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";

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
    queryFn: () =>
      apiClient.list<ResourcePool>(
        apiPath`/api/v1/clusters/${clusterId}/pools`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useResourcePool(clusterId: string, poolId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "pools", poolId],
    queryFn: () =>
      apiClient.get<ResourcePoolDetail>(
        apiPath`/api/v1/clusters/${clusterId}/pools/${poolId}`,
      ),
    enabled: clusterId.length > 0 && poolId.length > 0,
  });
}

export function useCreatePool(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { poolid: string; comment?: string }) =>
      apiClient.post(apiPath`/api/v1/clusters/${clusterId}/pools`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "pools"] });
    },
  });
}

export function useUpdatePool(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      poolid,
      ...data
    }: {
      poolid: string;
      comment?: string;
      vms?: string;
      storage?: string;
      delete?: string;
    }) =>
      apiClient.put(
        apiPath`/api/v1/clusters/${clusterId}/pools/${poolid}`,
        data,
      ),
    // Returned, not voided: the mutation stays pending until the pools are
    // read again, so the pool's member list never shows a member the PUT
    // already removed beside a Remove button that is enabled again (a second
    // removal of it would fail). invalidateQueries does not reject when the
    // refetch fails, so a failed re-read never turns this PUT into an error.
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["clusters", clusterId, "pools"] }),
  });
}

export function useDeletePool(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (poolId: string) =>
      apiClient.delete(apiPath`/api/v1/clusters/${clusterId}/pools/${poolId}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "pools"] });
    },
  });
}
