import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  CephStatus,
  CephOSD,
  CephOSDAction,
  CephOSDActionResponse,
  CephOSDPreflight,
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
    refetchInterval: 60_000,
  });
}

export function useCephOSDs(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "osds"],
    queryFn: () =>
      apiClient.list<CephOSD>(
        `/api/v1/clusters/${clusterId}/ceph/osds`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: 60_000,
  });
}

export function useCephPools(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "pools"],
    queryFn: () =>
      apiClient.list<CephPool>(
        `/api/v1/clusters/${clusterId}/ceph/pools`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: 60_000,
  });
}

export function useCephMonitors(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "monitors"],
    queryFn: () =>
      apiClient.list<CephMon>(
        `/api/v1/clusters/${clusterId}/ceph/monitors`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCephFS(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "fs"],
    queryFn: () =>
      apiClient.list<CephFS>(
        `/api/v1/clusters/${clusterId}/ceph/fs`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCephCrushRules(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "rules"],
    queryFn: () =>
      apiClient.list<CephCrushRule>(
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
      apiClient.list<CephClusterMetric>(
        `/api/v1/clusters/${clusterId}/ceph/metrics?timeframe=${timeframe}`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: 60_000,
  });
}

export function useCephOSDMetrics(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "osds", "metrics"],
    queryFn: () =>
      apiClient.list<CephOSDMetric>(
        `/api/v1/clusters/${clusterId}/ceph/osds/metrics`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCephPoolMetrics(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "pools", "metrics"],
    queryFn: () =>
      apiClient.list<CephPoolMetric>(
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

/**
 * Fetches the redundancy assessment for an OSD action. Kept out of the cache
 * (staleTime 0) because it is read at the moment the operator opens the confirm
 * dialog and a stale up/in count is exactly what it exists to prevent.
 */
export function useCephOSDPreflight(
  clusterId: string,
  osdId: number,
  action: CephOSDAction,
) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ceph", "osds", osdId, "preflight", action],
    queryFn: () =>
      apiClient.get<CephOSDPreflight>(
        `/api/v1/clusters/${clusterId}/ceph/osds/${String(osdId)}/preflight?action=${action}`,
      ),
    enabled: clusterId.length > 0,
    staleTime: 0,
    gcTime: 0,
    retry: false,
  });
}

interface OSDActionParams {
  clusterId: string;
  osdId: number;
  action: CephOSDAction;
}

export function useCephOSDAction() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, osdId, action }: OSDActionParams) =>
      apiClient.post<CephOSDActionResponse>(
        `/api/v1/clusters/${clusterId}/ceph/osds/${String(osdId)}/${action}`,
        {},
      ),
    onSuccess: (data, variables) => {
      const invalidate = () =>
        queryClient.invalidateQueries({
          queryKey: ["clusters", variables.clusterId, "ceph"],
        });

      // Membership changes are applied by the time the request returns.
      void invalidate();

      // Daemon actions only return a dispatched UPID, so the refetch above
      // still reads pre-action state. Without a second pass the row keeps its
      // stale Up badge until the 60s poll and the menu re-offers the action
      // that is already in flight — an easy way to dispatch a second stop.
      if (data.upid !== undefined && data.upid !== "") {
        setTimeout(() => void invalidate(), DAEMON_SETTLE_MS);
      }
    },
  });
}

/** Roughly how long a Ceph OSD daemon takes to report its new state. */
const DAEMON_SETTLE_MS = 5000;

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
