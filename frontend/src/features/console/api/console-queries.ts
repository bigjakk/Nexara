import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

export type ConsoleScopeType =
  | "node_shell"
  | "vm_serial"
  | "vm_vnc"
  | "ct_attach"
  | "ct_vnc";

export interface MintConsoleTokenParams {
  clusterId: string;
  node: string;
  type: ConsoleScopeType;
  /** Required for everything except node_shell. */
  vmid?: number;
}

export interface ConsoleTokenResponse {
  token: string;
  expires_in: number;
}

/**
 * Mint a short-lived (5 minute), scope-locked JWT for a single console
 * WebSocket upgrade. Call this immediately before opening /ws/console or
 * /ws/vnc — those endpoints reject regular access tokens (security review
 * fix #1: per-cluster RBAC enforcement).
 *
 * The backend POST /api/v1/auth/console-token enforces the per-cluster
 * RBAC check before issuing the token. The token is bound to the exact
 * (cluster, node, vmid, type) tuple supplied here; passing it to a
 * different upgrade is rejected.
 */
export async function mintConsoleToken(
  params: MintConsoleTokenParams,
): Promise<ConsoleTokenResponse> {
  const body: {
    cluster_id: string;
    node: string;
    type: ConsoleScopeType;
    vmid?: number;
  } = {
    cluster_id: params.clusterId,
    node: params.node,
    type: params.type,
  };
  if (params.vmid !== undefined) {
    body.vmid = params.vmid;
  }
  return apiClient.post<ConsoleTokenResponse>(
    "/api/v1/auth/console-token",
    body,
  );
}

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
