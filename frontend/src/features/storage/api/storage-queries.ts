import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { StorageResponse } from "@/types/api";
import type {
  StorageContentItem,
  StorageActionResponse,
  DiskResizeRequest,
  DiskMoveRequest,
} from "../types/storage";

// --- Storage pools for a cluster ---

export function useClusterStorage(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "storage"],
    queryFn: () =>
      apiClient.get<StorageResponse[]>(
        `/api/v1/clusters/${clusterId}/storage`,
      ),
    enabled: clusterId.length > 0,
  });
}

// --- Storage content listing ---

export function useStorageContent(clusterId: string, storageId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "storage", storageId, "content"],
    queryFn: () =>
      apiClient.get<StorageContentItem[]>(
        `/api/v1/clusters/${clusterId}/storage/${storageId}/content`,
      ),
    enabled: clusterId.length > 0 && storageId.length > 0,
  });
}

// --- Upload file ---

interface UploadParams {
  clusterId: string;
  storageId: string;
  content: "iso" | "vztmpl";
  file: File;
}

export function useUploadFile() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async ({ clusterId, storageId, content, file }: UploadParams) => {
      const formData = new FormData();
      formData.append("content", content);
      formData.append("file", file);

      const token = localStorage.getItem("access_token");
      const headers: Record<string, string> = {};
      if (token) {
        headers["Authorization"] = `Bearer ${token}`;
      }

      const res = await fetch(
        `/api/v1/clusters/${clusterId}/storage/${storageId}/upload`,
        {
          method: "POST",
          headers,
          body: formData,
        },
      );

      if (!res.ok) {
        const err = await res.json().catch(() => ({ message: res.statusText })) as { message: string };
        throw new Error(err.message);
      }

      return (await res.json()) as StorageActionResponse;
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: [
          "clusters",
          variables.clusterId,
          "storage",
          variables.storageId,
          "content",
        ],
      });
    },
  });
}

// --- Delete content ---

interface DeleteContentParams {
  clusterId: string;
  storageId: string;
  volume: string;
}

export function useDeleteContent() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, storageId, volume }: DeleteContentParams) =>
      apiClient.delete<StorageActionResponse>(
        `/api/v1/clusters/${clusterId}/storage/${storageId}/content/${encodeURIComponent(volume)}`,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: [
          "clusters",
          variables.clusterId,
          "storage",
          variables.storageId,
          "content",
        ],
      });
    },
  });
}

// --- Disk resize ---

interface ResizeDiskParams {
  clusterId: string;
  vmId: string;
  body: DiskResizeRequest;
}

export function useResizeDisk() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, vmId, body }: ResizeDiskParams) =>
      apiClient.post<StorageActionResponse>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/disks/resize`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms"],
      });
    },
  });
}

// --- Disk move ---

interface MoveDiskParams {
  clusterId: string;
  vmId: string;
  body: DiskMoveRequest;
}

export function useMoveDisk() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, vmId, body }: MoveDiskParams) =>
      apiClient.post<StorageActionResponse>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/disks/move`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms"],
      });
    },
  });
}
