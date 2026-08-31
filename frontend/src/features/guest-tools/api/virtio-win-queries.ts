import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  VirtioWinConfig,
  VirtioWinConfigRequest,
  VirtioWinDownload,
  VirtioWinDownloadRequest,
  VirtioWinDownloadResult,
  VirtioWinRelease,
} from "../types/virtio-win";

export const virtioWinKeys = {
  all: ["virtio-win"] as const,
  releases: () => [...virtioWinKeys.all, "releases"] as const,
  config: (clusterId: string) =>
    [...virtioWinKeys.all, "config", clusterId] as const,
  downloads: (clusterId: string) =>
    [...virtioWinKeys.all, "downloads", clusterId] as const,
};

/**
 * The upstream release catalog. Global rather than per-cluster — every cluster
 * pins against the same published set — and refreshed by the scheduler every
 * six hours, so it is cached generously.
 */
export function useVirtioWinReleases() {
  return useQuery({
    queryKey: virtioWinKeys.releases(),
    queryFn: () =>
      apiClient.list<VirtioWinRelease>("/api/v1/virtio-win/releases"),
    staleTime: 5 * 60_000,
  });
}

export function useVirtioWinConfig(clusterId: string) {
  return useQuery({
    queryKey: virtioWinKeys.config(clusterId),
    queryFn: () =>
      apiClient.get<VirtioWinConfig>(
        `/api/v1/clusters/${clusterId}/virtio-win/config`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useUpdateVirtioWinConfig(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (config: VirtioWinConfigRequest) =>
      apiClient.put<VirtioWinConfig>(
        `/api/v1/clusters/${clusterId}/virtio-win/config`,
        config,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: virtioWinKeys.config(clusterId),
      });
    },
  });
}

/**
 * Download history, newest first. Polled while anything is in flight: the
 * transfer is an ~837 MiB Proxmox task, so its status changes long after the
 * request that started it returned.
 */
export function useVirtioWinDownloads(clusterId: string) {
  return useQuery({
    queryKey: virtioWinKeys.downloads(clusterId),
    queryFn: () =>
      apiClient.list<VirtioWinDownload>(
        `/api/v1/clusters/${clusterId}/virtio-win/downloads`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: (query) => {
      const rows = query.state.data;
      if (!rows) return false;
      const active = rows.some(
        (r) => r.status === "pending" || r.status === "running",
      );
      return active ? 5_000 : false;
    },
  });
}

export function useDownloadVirtioWin(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: VirtioWinDownloadRequest) =>
      apiClient.post<VirtioWinDownloadResult>(
        `/api/v1/clusters/${clusterId}/virtio-win/download`,
        body,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: virtioWinKeys.downloads(clusterId),
      });
      void queryClient.invalidateQueries({
        queryKey: virtioWinKeys.config(clusterId),
      });
    },
  });
}
