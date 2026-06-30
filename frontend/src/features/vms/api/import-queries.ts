import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  EsxiSourceRequest,
  ImportMetadataResponse,
  ImportSourceContent,
  StartImportRequest,
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

// List importable volumes/guests on an import-capable storage pool.
export function useImportSourceContent(clusterId: string, storageId: string | null) {
  return useQuery({
    queryKey: ["clusters", clusterId, "import-sources", storageId, "content"],
    queryFn: () =>
      apiClient.get<ImportSourceContent>(
        `/api/v1/clusters/${clusterId}/vm-import-sources/${storageId ?? ""}/content`,
      ),
    enabled: clusterId.length > 0 && !!storageId,
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
        queryKey: ["clusters", variables.clusterId, "storage"],
      });
    },
  });
}
