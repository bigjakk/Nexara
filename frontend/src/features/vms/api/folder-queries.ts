import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  VMFolder,
  VMFolderListResponse,
} from "@/types/api";

const FOLDER_LIST_KEY = (clusterId: string) =>
  ["clusters", clusterId, "vm-folders"] as const;

export function useVMFolders(clusterId: string) {
  return useQuery({
    queryKey: FOLDER_LIST_KEY(clusterId),
    queryFn: () =>
      apiClient.get<VMFolderListResponse>(
        `/api/v1/clusters/${clusterId}/vm-folders`,
      ),
    enabled: clusterId.length > 0,
    staleTime: 15_000,
  });
}

interface CreateFolderParams {
  clusterId: string;
  name: string;
  parent_id: string | null;
}

export function useCreateVMFolder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, name, parent_id }: CreateFolderParams) =>
      apiClient.post<VMFolder>(`/api/v1/clusters/${clusterId}/vm-folders`, {
        name,
        parent_id,
      }),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: FOLDER_LIST_KEY(vars.clusterId) });
    },
  });
}

interface UpdateFolderParams {
  clusterId: string;
  folderId: string;
  name?: string;
  parent_id?: string | null;
}

export function useUpdateVMFolder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, folderId, name, parent_id }: UpdateFolderParams) => {
      const body: Record<string, unknown> = {};
      if (name !== undefined) body["name"] = name;
      if (parent_id !== undefined) body["parent_id"] = parent_id;
      return apiClient.patch<VMFolder>(
        `/api/v1/clusters/${clusterId}/vm-folders/${folderId}`,
        body,
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: FOLDER_LIST_KEY(vars.clusterId) });
    },
  });
}

interface DeleteFolderParams {
  clusterId: string;
  folderId: string;
}

export function useDeleteVMFolder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, folderId }: DeleteFolderParams) =>
      apiClient.delete<null>(
        `/api/v1/clusters/${clusterId}/vm-folders/${folderId}`,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: FOLDER_LIST_KEY(vars.clusterId) });
    },
  });
}

interface AssignVMParams {
  clusterId: string;
  vmId: string;
  folder_id: string | null;
}

export function useAssignVMToFolder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, vmId, folder_id }: AssignVMParams) =>
      apiClient.put<null>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/folder`,
        { folder_id },
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: FOLDER_LIST_KEY(vars.clusterId) });
    },
  });
}
