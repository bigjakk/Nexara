import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  CephStatus,
  CephOSD,
  CephPool,
  CephMon,
  CephFS,
  CephCrushRule,
  CephClusterMetric,
  CephOSDMetric,
  CephPoolMetric,
  CreatePoolRequest,
} from "../types/ceph";

// --- Live Proxmox data ---

export function useCephStatus(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "status"],
    queryFn: () =>
      apiClient.get<CephStatus>(
        `/api/v1/clusters/${clusterId}/ceph/status`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: 30000,
  });
}

export function useCephOSDs(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "osds"],
    queryFn: () =>
      apiClient.get<CephOSD[]>(
        `/api/v1/clusters/${clusterId}/ceph/osds`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: 30000,
  });
}

export function useCephPools(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "pools"],
    queryFn: () =>
      apiClient.get<CephPool[]>(
        `/api/v1/clusters/${clusterId}/ceph/pools`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: 30000,
  });
}

export function useCephMonitors(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "monitors"],
    queryFn: () =>
      apiClient.get<CephMon[]>(
        `/api/v1/clusters/${clusterId}/ceph/monitors`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCephFS(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "fs"],
    queryFn: () =>
      apiClient.get<CephFS[]>(
        `/api/v1/clusters/${clusterId}/ceph/fs`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCephCrushRules(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "rules"],
    queryFn: () =>
      apiClient.get<CephCrushRule[]>(
        `/api/v1/clusters/${clusterId}/ceph/rules`,
      ),
    enabled: clusterId.length > 0,
  });
}

// --- DB metrics ---

export function useCephMetrics(clusterId: string, timeframe: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "metrics", timeframe],
    queryFn: () =>
      apiClient.get<CephClusterMetric[]>(
        `/api/v1/clusters/${clusterId}/ceph/metrics?timeframe=${timeframe}`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: 30000,
  });
}

export function useCephOSDMetrics(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "osds", "metrics"],
    queryFn: () =>
      apiClient.get<CephOSDMetric[]>(
        `/api/v1/clusters/${clusterId}/ceph/osds/metrics`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCephPoolMetrics(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "pools", "metrics"],
    queryFn: () =>
      apiClient.get<CephPoolMetric[]>(
        `/api/v1/clusters/${clusterId}/ceph/pools/metrics`,
      ),
    enabled: clusterId.length > 0,
  });
}

// --- Mutations ---

interface CreatePoolParams {
  clusterId: string;
  body: CreatePoolRequest;
}

export function useCreateCephPool() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, body }: CreatePoolParams) =>
      apiClient.post<{ status: string; name: string }>(
        `/api/v1/clusters/${clusterId}/ceph/pools`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "ceph", "pools"],
      });
    },
  });
}

interface DeletePoolParams {
  clusterId: string;
  poolName: string;
}

export function useDeleteCephPool() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, poolName }: DeletePoolParams) =>
      apiClient.delete<{ status: string; name: string }>(
        `/api/v1/clusters/${clusterId}/ceph/pools/${encodeURIComponent(poolName)}`,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "ceph", "pools"],
      });
    },
  });
}
