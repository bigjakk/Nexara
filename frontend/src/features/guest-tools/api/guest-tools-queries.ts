import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  GuestToolsConfig,
  GuestToolsConfigRequest,
  GuestToolsDetectResponse,
  GuestToolsGuest,
  GuestToolsPolicyRequest,
  GuestToolsUpdateRequest,
  GuestToolsUpdateResponse,
} from "../types/guest-tools";

export const guestToolsKeys = {
  all: ["guest-tools"] as const,
  config: (clusterId: string) =>
    [...guestToolsKeys.all, "config", clusterId] as const,
  fleet: (clusterId: string) =>
    [...guestToolsKeys.all, "fleet", clusterId] as const,
};

export function useGuestToolsConfig(clusterId: string) {
  return useQuery({
    queryKey: guestToolsKeys.config(clusterId),
    queryFn: () =>
      apiClient.get<GuestToolsConfig>(
        `/api/v1/clusters/${clusterId}/guest-tools/config`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useUpdateGuestToolsConfig(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (config: GuestToolsConfigRequest) =>
      apiClient.put<GuestToolsConfig>(
        `/api/v1/clusters/${clusterId}/guest-tools/config`,
        config,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: guestToolsKeys.all });
    },
  });
}

/**
 * The Windows fleet with its guest tools state.
 *
 * Polled while anything is in flight: a staged install fires on the guest's own
 * reboot, so its outcome arrives long after the request that staged it.
 */
export function useGuestToolsFleet(clusterId: string, enabled = true) {
  return useQuery({
    queryKey: guestToolsKeys.fleet(clusterId),
    queryFn: () =>
      apiClient.list<GuestToolsGuest>(
        `/api/v1/clusters/${clusterId}/guest-tools/guests`,
      ),
    enabled: enabled && clusterId.length > 0,
    refetchInterval: (query) => {
      const rows = query.state.data;
      if (!rows) return false;
      const active = rows.some(
        (g) =>
          g.stage === "staging" || g.stage === "staged" || g.stage === "running",
      );
      return active ? 15_000 : false;
    },
  });
}

/**
 * One guest's row out of the cluster fleet.
 *
 * Derived from the fleet query rather than a per-guest endpoint: the list is one
 * row per Windows guest, the target / up-to-date / needs-update judgements are
 * already computed server-side, and both the VM tab and the guest agent summary
 * share a single cache entry instead of each fetching their own.
 */
export function useGuestToolsGuest(
  clusterId: string,
  vmid: number,
  enabled = true,
) {
  // enabled matters here: the rules of hooks force callers to call this before
  // they know whether the guest is Windows, and without it every Linux VM's
  // detail page would fire a cluster-wide guest-tools request — a 403 on every
  // page view for anyone whose role lacks view:guest_tools.
  const query = useGuestToolsFleet(clusterId, enabled);
  return {
    ...query,
    guest: query.data?.find((g) => g.vmid === vmid),
  };
}

export function useSetGuestToolsPolicy(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      vmid,
      policy,
    }: {
      vmid: number;
      policy: GuestToolsPolicyRequest;
    }) =>
      apiClient.put(
        `/api/v1/clusters/${clusterId}/guest-tools/guests/${String(vmid)}/policy`,
        policy,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: guestToolsKeys.all });
    },
  });
}

export function useDetectGuestTools(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (vmid: number) =>
      apiClient.post<GuestToolsDetectResponse>(
        `/api/v1/clusters/${clusterId}/guest-tools/guests/${String(vmid)}/detect`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: guestToolsKeys.all });
    },
  });
}

export function useStageGuestToolsUpdate(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      vmid,
      body,
    }: {
      vmid: number;
      body: GuestToolsUpdateRequest;
    }) =>
      apiClient.post<GuestToolsUpdateResponse>(
        `/api/v1/clusters/${clusterId}/guest-tools/guests/${String(vmid)}/update`,
        body,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: guestToolsKeys.all });
    },
  });
}

export function useCancelGuestToolsUpdate(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (vmid: number) =>
      apiClient.delete(
        `/api/v1/clusters/${clusterId}/guest-tools/guests/${String(vmid)}/update`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: guestToolsKeys.all });
    },
  });
}
