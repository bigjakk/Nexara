import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type {
  NutProbeResult,
  NutServer,
  NutServerRequest,
  UpsDevice,
  UpsEvent,
  UpsMetrics,
  UpsRange,
} from "../types/ups";

// UPS monitoring is global scope: one NUT server can feed nodes of several
// clusters, so nothing here is per cluster. Readings move between polls
// without an event, so the device queries poll while the page is visible; a
// change of state also arrives as a ups_change event (useEventInvalidation).

const DEVICE_REFRESH_MS = 10_000;

export const upsKeys = {
  servers: ["nut-servers"] as const,
  devices: ["ups-devices"] as const,
  device: (id: string) => ["ups-device", id] as const,
  metrics: (id: string, range: UpsRange) => ["ups-device", id, "metrics", range] as const,
  events: (upsId?: string) => ["ups-events", upsId ?? "all"] as const,
};

export function useNutServers() {
  return useQuery({
    queryKey: upsKeys.servers,
    queryFn: () => apiClient.list<NutServer>(apiPath`/api/v1/nut-servers`),
  });
}

export function useUpsDevices(enabled = true) {
  return useQuery({
    queryKey: upsKeys.devices,
    queryFn: () => apiClient.list<UpsDevice>(apiPath`/api/v1/ups-devices`),
    refetchInterval: DEVICE_REFRESH_MS,
    enabled,
  });
}

export function useUpsDevice(id: string) {
  return useQuery({
    queryKey: upsKeys.device(id),
    queryFn: () => apiClient.get<UpsDevice>(apiPath`/api/v1/ups-devices/${id}`),
    refetchInterval: DEVICE_REFRESH_MS,
    enabled: id !== "",
  });
}

export function useUpsMetrics(id: string, range: UpsRange) {
  return useQuery({
    queryKey: upsKeys.metrics(id, range),
    queryFn: () =>
      apiClient.get<UpsMetrics>(
        apiPath`/api/v1/ups-devices/${id}/metrics?range=${range}`,
      ),
    refetchInterval: 60_000,
    enabled: id !== "",
  });
}

/** How many events the lists read: the newest page, with no paging yet. */
export const UPS_EVENTS_LIMIT = 100;

export function useUpsEvents(upsId?: string) {
  return useQuery({
    queryKey: upsKeys.events(upsId),
    queryFn: () =>
      apiClient.list<UpsEvent>(
        upsId === undefined
          ? apiPath`/api/v1/ups-events?limit=${String(UPS_EVENTS_LIMIT)}`
          : apiPath`/api/v1/ups-events?ups_id=${upsId}&limit=${String(UPS_EVENTS_LIMIT)}`,
      ),
    refetchInterval: 60_000,
  });
}

/**
 * Opts a mutation out of the app's global error toast, for a caller that shows
 * the failure itself: the server dialog (which also toasts it once it is gone,
 * through useSaveOutcome) and a server row's Test. Anything that relies on the
 * toast must not use it, or its failure goes unreported.
 */
const errorsHandledLocally = { onError: () => undefined };

function useInvalidateUps() {
  const queryClient = useQueryClient();
  return () => {
    void queryClient.invalidateQueries({ queryKey: upsKeys.servers });
    void queryClient.invalidateQueries({ queryKey: upsKeys.devices });
    void queryClient.invalidateQueries({ queryKey: ["ups-events"] });
  };
}

export function useCreateNutServer() {
  const invalidate = useInvalidateUps();
  return useMutation({
    mutationFn: (req: NutServerRequest) =>
      apiClient.post<NutServer>(apiPath`/api/v1/nut-servers`, req),
    onSuccess: invalidate,
    ...errorsHandledLocally,
  });
}

export function useUpdateNutServer() {
  const invalidate = useInvalidateUps();
  return useMutation({
    mutationFn: ({ id, ...body }: NutServerRequest & { id: string }) =>
      apiClient.put<NutServer>(apiPath`/api/v1/nut-servers/${id}`, body),
    onSuccess: invalidate,
    ...errorsHandledLocally,
  });
}

export function useDeleteNutServer() {
  const invalidate = useInvalidateUps();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete<never>(apiPath`/api/v1/nut-servers/${id}`),
    onSuccess: invalidate,
  });
}

/**
 * Probes an unsaved connection. A mutation, not a query: it connects out and
 * must run only when the operator asks.
 */
export function useProbeNutServer() {
  return useMutation({
    mutationFn: (req: NutServerRequest) =>
      apiClient.post<NutProbeResult>(apiPath`/api/v1/nut-servers/probe`, req),
    ...errorsHandledLocally,
  });
}

export function useTestNutServer() {
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post<NutProbeResult>(apiPath`/api/v1/nut-servers/${id}/test`),
    ...errorsHandledLocally,
  });
}
