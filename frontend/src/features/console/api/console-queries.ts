import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

export interface ISOImage {
  volid: string;
  storage: string;
  name: string;
  size: number;
  ctime: number;
}

export function useNodeISOs(
  clusterId: string,
  nodeName: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "isos"],
    queryFn: () =>
      apiClient.get<ISOImage[]>(
        `/api/v1/clusters/${clusterId}/nodes/${nodeName}/isos`,
      ),
    enabled: enabled && clusterId.length > 0 && nodeName.length > 0,
    staleTime: 30_000,
  });
}

interface MountISOParams {
  clusterId: string;
  vmId: string;
  volid: string; // "local:iso/file.iso" or "none" to eject
}

export function useMountISO() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, vmId, volid }: MountISOParams) =>
      apiClient.post<{ status: string; device: string }>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/media`,
        { volid },
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: [
          "clusters",
          variables.clusterId,
          "vms",
          variables.vmId,
          "config",
        ],
      });
    },
  });
}
