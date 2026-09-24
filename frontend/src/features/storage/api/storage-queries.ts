import {
  type QueryClient,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { apiClient, openApiRequest } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { signedInUserID, usePBSKeyStore } from "@/stores/pbs-key-store";
import type { ClusterResponse, StorageResponse } from "@/types/api";
import type {
  StorageContentItem,
  StorageActionResponse,
  StorageConfigResponse,
  CreateStorageRequest,
  UpdateStorageRequest,
  StorageWriteResponse,
  OCIPullRequest,
  DownloadURLRequest,
  DownloadApplianceRequest,
  ApplianceTemplate,
  ISCSITarget,
} from "../types/storage";

// --- Storage pools for a cluster ---

export function useClusterStorage(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "storage"],
    queryFn: () =>
      apiClient.list<StorageResponse>(
        apiPath`/api/v1/clusters/${clusterId}/storage`,
      ),
    enabled: clusterId.length > 0,
    staleTime: 30_000,
    refetchInterval: 60_000,
  });
}

// --- Storage content listing ---

export function useStorageContent(clusterId: string, storageId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "storage", storageId, "content"],
    queryFn: () =>
      apiClient.list<StorageContentItem>(
        apiPath`/api/v1/clusters/${clusterId}/storage/${storageId}/content`,
      ),
    enabled: clusterId.length > 0 && storageId.length > 0,
  });
}

// --- Upload file with progress ---

interface UploadParams {
  clusterId: string;
  storageId: string;
  content: "iso" | "vztmpl" | "import";
  file: File;
  onProgress?: (percent: number) => void;
}

async function uploadFile({
  clusterId,
  storageId,
  content,
  file,
  onProgress,
}: UploadParams): Promise<StorageActionResponse> {
  // openApiRequest checks the path, opens the request and sets its
  // Authorization header from a fresh access token — an XHR's must be set
  // before send(), and it is too late to refresh once a 401 lands.
  const xhr = await openApiRequest(
    "POST",
    apiPath`/api/v1/clusters/${clusterId}/storage/${storageId}/upload`,
  );
  return new Promise((resolve, reject) => {
    xhr.withCredentials = true;

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
          reject(
            new Error(err.message ?? `Upload failed (${String(xhr.status)})`),
          );
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
    formData.append("filesize", String(file.size));
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
        apiPath`/api/v1/clusters/${clusterId}/storage/${storageId}/content/${volume}`,
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

// --- Storage config (for editing) ---

export function useStorageConfig(clusterId: string, storageId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "storage", storageId, "config"],
    queryFn: () =>
      apiClient.get<StorageConfigResponse>(
        apiPath`/api/v1/clusters/${clusterId}/storage/${storageId}/config`,
      ),
    enabled: clusterId.length > 0 && storageId.length > 0,
  });
}

// --- Create storage ---

interface CreateStorageParams {
  clusterId: string;
  data: CreateStorageRequest;
}

// Both storage writes can carry a PBS encryption key: a pasted one in their
// variables, a generated one in their data. gcTime 0 drops the finished
// mutation from TanStack's cache as soon as nothing observes it, rather than
// the default five minutes later — the dialogs reset theirs once the save
// completes, by which time any generated key is in usePBSKeyStore.
const STORAGE_WRITE_GC_TIME = 0;

interface WriteFiling {
  owner: string | undefined;
  clusterName: string | null;
}

/**
 * What a storage write's key would be filed under, taken by the hooks'
 * onMutate before the request goes out: the user signed in then, whom alone
 * a generated key is shown to (usePBSKeyStore) — by the time the answer comes
 * the session may have ended or passed to someone else — and the name of the
 * cluster the storage is on, from the cluster list AppShell already keeps
 * loaded, so the must-save dialog can say which cluster the key is for.
 */
function writeFiling(queryClient: QueryClient, clusterId: string): WriteFiling {
  const clusters = queryClient.getQueryData<ClusterResponse[]>(["clusters"]);
  return {
    owner: signedInUserID(),
    clusterName: clusters?.find((c) => c.id === clusterId)?.name ?? null,
  };
}

/**
 * Hands a key Proxmox generated for this write to the must-save dialog.
 *
 * Whether one was generated is decided from what was sent — encryption-key
 * "autogen" — never from the response: a pasted key must not open the dialog,
 * even from a response that echoed it. The API drops that echo already
 * (proxmox.storageWriteResult); this is the second line.
 *
 * A write sent with no one signed in has no one to show a key to, so it is
 * not queued at all. The storage dialogs are reachable only signed in; this
 * only keeps such a key from being shown to whoever signs in next.
 *
 * Called from the hooks' own onSuccess, not from the caller's per-call one.
 * TanStack runs a hook's callbacks from the mutation itself, so the key is
 * delivered even when the dialog that started the save closed, or the page
 * changed, before the answer came; a per-call callback reaches only an observer
 * that is still there.
 */
function deliverGeneratedKey(
  sent: Record<string, string>,
  response: StorageWriteResponse,
  cluster: string,
  { owner, clusterName }: WriteFiling,
) {
  if (sent["encryption-key"] !== "autogen") return;
  if (owner === undefined) return;
  usePBSKeyStore.getState().deliver({
    owner,
    cluster,
    clusterName,
    storage: response.storage,
    keyText: response.generated_encryption_key ?? "",
  });
}

export function useCreateStorage() {
  const queryClient = useQueryClient();

  return useMutation({
    gcTime: STORAGE_WRITE_GC_TIME,
    mutationFn: ({ clusterId, data }: CreateStorageParams) =>
      apiClient.post<StorageWriteResponse>(
        apiPath`/api/v1/clusters/${clusterId}/storage`,
        data,
      ),
    onMutate: (variables) => writeFiling(queryClient, variables.clusterId),
    onSuccess: (response, variables, filing) => {
      deliverGeneratedKey(
        variables.data.params,
        response,
        variables.clusterId,
        filing,
      );
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "storage"],
      });
    },
  });
}

// --- Update storage ---

interface UpdateStorageParams {
  clusterId: string;
  storageId: string;
  data: UpdateStorageRequest;
}

export function useUpdateStorage() {
  const queryClient = useQueryClient();

  return useMutation({
    gcTime: STORAGE_WRITE_GC_TIME,
    mutationFn: ({ clusterId, storageId, data }: UpdateStorageParams) =>
      apiClient.put<StorageWriteResponse>(
        apiPath`/api/v1/clusters/${clusterId}/storage/${storageId}`,
        data,
      ),
    onMutate: (variables) => writeFiling(queryClient, variables.clusterId),
    onSuccess: (response, variables, filing) => {
      deliverGeneratedKey(
        variables.data.params,
        response,
        variables.clusterId,
        filing,
      );
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "storage"],
      });
    },
  });
}

// --- Pull OCI image ---

interface PullOCIParams {
  clusterId: string;
  storageId: string;
  data: OCIPullRequest;
}

export function usePullOCIImage() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, storageId, data }: PullOCIParams) =>
      apiClient.post<StorageActionResponse>(
        apiPath`/api/v1/clusters/${clusterId}/storage/${storageId}/oci-pull`,
        data,
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

// --- Download URL to storage ---

interface DownloadURLParams {
  clusterId: string;
  storageId: string;
  data: DownloadURLRequest;
}

export function useDownloadURL() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, storageId, data }: DownloadURLParams) =>
      apiClient.post<StorageActionResponse>(
        apiPath`/api/v1/clusters/${clusterId}/storage/${storageId}/download-url`,
        data,
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

// --- Appliance catalog ---

export function useAppliances(clusterId: string, enabled = true) {
  return useQuery({
    queryKey: ["clusters", clusterId, "appliances"],
    queryFn: () =>
      apiClient.list<ApplianceTemplate>(
        apiPath`/api/v1/clusters/${clusterId}/appliances`,
      ),
    enabled: enabled && clusterId.length > 0,
    staleTime: 60 * 60 * 1000,
    gcTime: 24 * 60 * 60 * 1000,
  });
}

// --- Download appliance ---

interface DownloadApplianceParams {
  clusterId: string;
  storageId: string;
  data: DownloadApplianceRequest;
}

export function useDownloadAppliance() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, storageId, data }: DownloadApplianceParams) =>
      apiClient.post<StorageActionResponse>(
        apiPath`/api/v1/clusters/${clusterId}/storage/${storageId}/appliances`,
        data,
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

// --- Delete storage ---

interface DeleteStorageParams {
  clusterId: string;
  storageId: string;
}

export function useDeleteStorage() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, storageId }: DeleteStorageParams) =>
      apiClient.delete<{ status: string; storage: string }>(
        apiPath`/api/v1/clusters/${clusterId}/storage/${storageId}`,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "storage"],
      });
    },
  });
}

// --- iSCSI target discovery ---

/**
 * Discovers the target IQNs advertised by an iSCSI portal, so the storage
 * dialogs can offer them instead of making the operator type one.
 *
 * The scan makes a Proxmox node open an outbound connection to the portal, so
 * the endpoint requires manage:storage and the query stays disabled until the
 * caller passes a portal worth probing. Failures are surfaced, not retried —
 * an unreachable portal should fall back to manual entry immediately.
 */
export function useISCSITargets(clusterId: string, portal: string) {
  const trimmed = portal.trim();
  return useQuery({
    queryKey: ["clusters", clusterId, "scan", "iscsi", trimmed],
    queryFn: () =>
      apiClient.list<ISCSITarget>(
        apiPath`/api/v1/clusters/${clusterId}/scan/iscsi?portal=${trimmed}`,
      ),
    enabled: clusterId.length > 0 && trimmed.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}
