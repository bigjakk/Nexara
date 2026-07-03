import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  EsxiSourceRequest,
  ImportMetadataResponse,
  ImportSource,
  ImportSourceContent,
  StartImportRequest,
  URLMetadataResponse,
  VMImportJob,
} from "@/types/api";

// Parse an importable source (OVA/OVF/ESXi guest) into a pre-filled guest definition.
export function useImportMetadata() {
  return useMutation({
    mutationFn: ({
      clusterId,
      node,
      storage,
      volume,
    }: {
      clusterId: string;
      node: string;
      storage: string;
      volume: string;
    }) =>
      apiClient.post<ImportMetadataResponse>(
        `/api/v1/clusters/${clusterId}/import-metadata`,
        { node, storage, volume },
      ),
  });
}

// List the cluster's import-capable storages + ESXi sources, deduplicated (shared storages
// once, non-shared per node), each paired with an online node to browse it from.
export function useImportSources(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "import-sources"],
    queryFn: () =>
      apiClient.get<ImportSource[]>(
        `/api/v1/clusters/${clusterId}/vm-import-sources`,
      ),
    enabled: clusterId.length > 0,
  });
}

// List importable volumes/guests on a storage as seen from a specific (online) node.
export function useImportSourceContent(
  clusterId: string,
  storage: string | null,
  node: string | null,
) {
  return useQuery({
    queryKey: ["clusters", clusterId, "import-content", storage, node],
    queryFn: () => {
      const params = new URLSearchParams({
        storage: storage ?? "",
        node: node ?? "",
      });
      return apiClient.get<ImportSourceContent>(
        `/api/v1/clusters/${clusterId}/vm-import-sources/content?${params.toString()}`,
      );
    },
    enabled: clusterId.length > 0 && !!storage && !!node,
  });
}

// Ask Proxmox to detect a download's filename/size before staging it (the PVE "Query URL").
export function useQueryURLMetadata() {
  return useMutation({
    mutationFn: ({
      clusterId,
      node,
      url,
    }: {
      clusterId: string;
      node: string;
      url: string;
    }) => {
      const params = new URLSearchParams({ url });
      if (node) params.set("node", node);
      return apiClient.get<URLMetadataResponse>(
        `/api/v1/clusters/${clusterId}/query-url-metadata?${params.toString()}`,
      );
    },
  });
}

export function useStartImport() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, body }: { clusterId: string; body: StartImportRequest }) =>
      apiClient.post<VMImportJob>(`/api/v1/clusters/${clusterId}/vm-imports`, body),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vm-imports"],
      });
    },
  });
}

export function useImportJobs(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "vm-imports"],
    queryFn: () =>
      apiClient.get<VMImportJob[]>(`/api/v1/clusters/${clusterId}/vm-imports`),
    enabled: clusterId.length > 0,
    refetchInterval: 10_000,
  });
}

export function useCancelImport() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      id,
      deleteVm,
    }: {
      clusterId: string;
      id: string;
      deleteVm: boolean;
    }) =>
      apiClient.post<VMImportJob>(
        `/api/v1/clusters/${clusterId}/vm-imports/${id}/cancel`,
        { delete_vm: deleteVm },
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vm-imports"],
      });
    },
  });
}

export function useRegisterEsxiSource() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, body }: { clusterId: string; body: EsxiSourceRequest }) =>
      apiClient.post<{ status: string; storage: string }>(
        `/api/v1/clusters/${clusterId}/vm-import-sources/esxi`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "import-sources"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "storage"],
      });
    },
  });
}

export function useDeleteImportSource() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, storage }: { clusterId: string; storage: string }) =>
      apiClient.delete<{ status: string; storage: string }>(
        `/api/v1/clusters/${clusterId}/vm-import-sources/${encodeURIComponent(storage)}`,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "import-sources"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "storage"],
      });
    },
  });
}

export function useEnableImportContent() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, storage }: { clusterId: string; storage: string }) =>
      apiClient.post<{ status: string; storage: string; content: string }>(
        `/api/v1/clusters/${clusterId}/vm-import-sources/enable-content`,
        { storage },
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "import-sources"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "storage"],
      });
    },
  });
}
