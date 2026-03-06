import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { StorageResponse } from "@/types/api";
import type {
  StorageContentItem,
  StorageActionResponse,
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
    staleTime: 30_000,
    refetchInterval: 30_000,
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

// --- Upload file with progress ---

interface UploadParams {
  clusterId: string;
  storageId: string;
  content: "iso" | "vztmpl";
  file: File;
  onProgress?: (percent: number) => void;
}

function uploadFile({ clusterId, storageId, content, file, onProgress }: UploadParams): Promise<StorageActionResponse> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `/api/v1/clusters/${clusterId}/storage/${storageId}/upload`);

    const token = localStorage.getItem("access_token");
    if (token) {
      xhr.setRequestHeader("Authorization", `Bearer ${token}`);
    }

    xhr.upload.addEventListener("progress", (e) => {
      if (e.lengthComputable && onProgress) {
        onProgress(Math.round((e.loaded / e.total) * 100));
      }
    });

    xhr.addEventListener("load", () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText) as StorageActionResponse);
        } catch {
          resolve({ upid: "", status: "completed" });
        }
      } else {
        try {
          const err = JSON.parse(xhr.responseText) as { message?: string };
          reject(new Error(err.message ?? `Upload failed (${String(xhr.status)})`));
        } catch {
          reject(new Error(`Upload failed (${String(xhr.status)})`));
        }
      }
    });

    xhr.addEventListener("error", () => {
      reject(new Error("Network error during upload"));
    });

    xhr.addEventListener("abort", () => {
      reject(new Error("Upload aborted"));
    });

    const formData = new FormData();
    formData.append("content", content);
    formData.append("file", file);
    xhr.send(formData);
  });
}

export function useUploadFile() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (params: UploadParams) => uploadFile(params),
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

