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
  /**
   * When true, the backend skips the audit-log entry for this mint and only
   * slog's it. Used by background previews (VM thumbnails) so they don't
   * flood the activity feed on every page visit. Honoured only for
   * vm_vnc / ct_vnc — user-initiated console types always audit.
   */
  silent?: boolean;
}

export interface ConsoleTokenResponse {
  token: string;
  expires_in: number;
}

/**
 * Mint a short-lived (60 second), scope-locked JWT for a single console
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
    silent?: boolean;
  } = {
    cluster_id: params.clusterId,
    node: params.node,
    type: params.type,
  };
  if (params.vmid !== undefined) {
    body.vmid = params.vmid;
  }
  if (params.silent === true) {
    body.silent = true;
  }
  return apiClient.post<ConsoleTokenResponse>(
    "/api/v1/auth/console-token",
    body,
  );
}

/**
 * Mint a short-lived (60 second) JWT for the generic /ws hub upgrade.
 *
 * Per remediation 2.7, the long-lived access token is no longer accepted
 * on any WebSocket upgrade — every WS connection mints a single-purpose
 * scoped JWT first. The hub token is the equivalent of mintConsoleToken
 * for non-console subscriptions (cluster metrics, alerts, events, etc.).
 *
 * The token is delivered to the server via
 * `Sec-WebSocket-Protocol: nexara.token, nexara.token.<jwt>` so it never
 * appears in the URL — keeping it out of proxy access logs, browser
 * history, and Referer headers.
 */
export async function mintWSHubToken(): Promise<ConsoleTokenResponse> {
  return apiClient.post<ConsoleTokenResponse>("/api/v1/auth/ws-token");
}

/**
 * Build the `protocols` argument for `new WebSocket(url, protocols)` from
 * a scoped JWT. The first entry is the static negotiation marker the
 * server echoes back; the second carries the JWT. Both halves must be
 * sent — the browser fails the connection (code 1006) if the server
 * doesn't acknowledge one of the requested subprotocols.
 *
 * Exposed as a helper so all four console components and the hub store
 * use the same protocol shape — drift here would silently break auth.
 */
export function wsAuthProtocols(token: string): [string, string] {
  return ["nexara.token", "nexara.token." + token];
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
