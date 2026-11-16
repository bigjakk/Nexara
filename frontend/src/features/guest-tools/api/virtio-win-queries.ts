import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { errorsHandledLocally } from "@/hooks/useSaveOutcome";
import type {
  VirtioWinCheckResult,
  VirtioWinConfig,
  VirtioWinConfigRequest,
  VirtioWinDownload,
  VirtioWinDownloadRequest,
  VirtioWinDownloadResult,
  VirtioWinMirror,
  VirtioWinMirrorRequest,
  VirtioWinRelease,
} from "../types/virtio-win";

export const virtioWinKeys = {
  all: ["virtio-win"] as const,
  releases: () => [...virtioWinKeys.all, "releases"] as const,
  config: (clusterId: string) =>
    [...virtioWinKeys.all, "config", clusterId] as const,
  downloads: (clusterId: string) =>
    [...virtioWinKeys.all, "downloads", clusterId] as const,
  mirror: () => [...virtioWinKeys.all, "mirror"] as const,
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
      apiClient.list<VirtioWinRelease>(apiPath`/api/v1/virtio-win/releases`),
    staleTime: 5 * 60_000,
  });
}

export function useVirtioWinConfig(clusterId: string) {
  return useQuery({
    queryKey: virtioWinKeys.config(clusterId),
    queryFn: () =>
      apiClient.get<VirtioWinConfig>(
        apiPath`/api/v1/clusters/${clusterId}/virtio-win/config`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useUpdateVirtioWinConfig(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    ...errorsHandledLocally,
    mutationFn: (config: VirtioWinConfigRequest) =>
      apiClient.put<VirtioWinConfig>(
        apiPath`/api/v1/clusters/${clusterId}/virtio-win/config`,
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
        apiPath`/api/v1/clusters/${clusterId}/virtio-win/downloads`,
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
    ...errorsHandledLocally,
    mutationFn: (body: VirtioWinDownloadRequest) =>
      apiClient.post<VirtioWinDownloadResult>(
        apiPath`/api/v1/clusters/${clusterId}/virtio-win/download`,
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

/**
 * The instance-wide download source. Read is open to view:storage so an
 * operator looking at a failed check can see where it was pointed; writing it
 * needs manage:settings, because one write repoints every cluster.
 */
export function useVirtioWinMirror() {
  return useQuery({
    queryKey: virtioWinKeys.mirror(),
    queryFn: () =>
      apiClient.get<VirtioWinMirror>(apiPath`/api/v1/virtio-win/mirror`),
    staleTime: 5 * 60_000,
  });
}

export function useUpdateVirtioWinMirror() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: VirtioWinMirrorRequest) =>
      apiClient.put<VirtioWinMirror>(apiPath`/api/v1/virtio-win/mirror`, body),
    // Its one caller asks a confirm-required answer as a prompt rather than
    // reporting it as a failure, and settles the save through useSaveOutcome,
    // which reports a failure that lands after the card has gone.
    ...errorsHandledLocally,
    onSuccess: () => {
      // Every virtio-win key at once — `all` is the prefix the rest are built
      // on. The catalog was discovered against the previous source, and every
      // cluster config carries the resolved source_url, so both are stale the
      // moment this lands along with the mirror itself.
      void queryClient.invalidateQueries({ queryKey: virtioWinKeys.all });
    },
  });
}

/**
 * Runs a cluster's scheduled check immediately. Distinct from a download: this
 * refreshes the catalog and fetches only if the target is actually missing,
 * which is what makes a just-saved schedule or source verifiable without
 * waiting for its next slot.
 */
export function useCheckVirtioWinNow(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    ...errorsHandledLocally,
    mutationFn: () =>
      apiClient.post<VirtioWinCheckResult>(
        apiPath`/api/v1/clusters/${clusterId}/virtio-win/check`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: virtioWinKeys.config(clusterId),
      });
      void queryClient.invalidateQueries({
        queryKey: virtioWinKeys.downloads(clusterId),
      });
      void queryClient.invalidateQueries({
        queryKey: virtioWinKeys.releases(),
      });
    },
  });
}
