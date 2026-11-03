import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { reportUpsRun } from "../lib/ups-runs";
import type {
  NutProbeResult,
  NutServer,
  NutServerRequest,
  UpsCommandRun,
  UpsDevice,
  UpsEvent,
  UpsFeed,
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
  // Under the device's key, so the ups_change event that ends a run refreshes it.
  runs: (id: string) => ["ups-device", id, "runs"] as const,
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

/**
 * A UPS's newest command runs and variable sets. A run still waiting on its
 * driver is asked about every few seconds as well, in case the event that
 * ends it is missed.
 */
export function useUpsCommandRuns(id: string) {
  return useQuery({
    queryKey: upsKeys.runs(id),
    queryFn: () => apiClient.list<UpsCommandRun>(apiPath`/api/v1/ups-devices/${id}/command-runs`),
    refetchInterval: (query) =>
      query.state.data?.some((r) => r.status === "sent" || r.status === "pending") === true ? 3_000 : false,
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

/** Something a UPS powers, added by hand: a node, or a label for another load. */
export interface UpsFeedRequest {
  feed_cluster_id?: string;
  node_name?: string;
  label?: string;
}

function useInvalidateDevice(upsId: string) {
  const queryClient = useQueryClient();
  return () => {
    void queryClient.invalidateQueries({ queryKey: upsKeys.devices });
    void queryClient.invalidateQueries({ queryKey: upsKeys.device(upsId) });
  };
}

/** Its failure is shown in the dialog that adds it, so it skips the global toast. */
export function useAddUpsFeed(upsId: string) {
  const invalidate = useInvalidateDevice(upsId);
  return useMutation({
    mutationFn: (req: UpsFeedRequest) => apiClient.post<UpsFeed>(apiPath`/api/v1/ups-devices/${upsId}/feeds`, req),
    onSuccess: invalidate,
    ...errorsHandledLocally,
  });
}

export function useRemoveUpsFeed(upsId: string) {
  const invalidate = useInvalidateDevice(upsId);
  return useMutation({
    mutationFn: (feedId: string) => apiClient.delete<never>(apiPath`/api/v1/ups-devices/${upsId}/feeds/${feedId}`),
    onSuccess: invalidate,
  });
}

/** Whether a node shuts down some way Nexara cannot see (decision P11). */
export function useSetUpsFeedProtectedElsewhere(upsId: string) {
  const invalidate = useInvalidateDevice(upsId);
  return useMutation({
    mutationFn: ({ feedId, protectedElsewhere }: { feedId: string; protectedElsewhere: boolean }) =>
      apiClient.put<UpsFeed>(apiPath`/api/v1/ups-devices/${upsId}/feeds/${feedId}`, {
        protected_elsewhere: protectedElsewhere,
      }),
    onSuccess: invalidate,
  });
}

/** The node a NUT server runs on, or detect to hand it back to the poller. */
export function useSetNutHostNode(serverId: string, upsId: string) {
  const invalidate = useInvalidateDevice(upsId);
  return useMutation({
    mutationFn: (body: { host_cluster_id: string; node_name: string } | { detect: true }) =>
      apiClient.put<never>(apiPath`/api/v1/nut-servers/${serverId}/host-node`, body),
    onSuccess: invalidate,
  });
}

function useInvalidateRuns(upsId: string) {
  const queryClient = useQueryClient();
  return () => {
    void queryClient.invalidateQueries({ queryKey: upsKeys.runs(upsId) });
    void queryClient.invalidateQueries({ queryKey: upsKeys.servers });
  };
}

/**
 * Runs an instant command. The answer is the run, which may already have
 * failed: upsd's refusal is a run's outcome, not an HTTP error. It is reported
 * here, not by the caller, so a tab switch before upsd answers loses nothing.
 */
export function useRunUpsCommand(upsId: string) {
  const invalidate = useInvalidateRuns(upsId);
  return useMutation({
    mutationFn: (req: { command: string; confirm?: string }) =>
      apiClient.post<UpsCommandRun>(apiPath`/api/v1/ups-devices/${upsId}/commands`, req),
    onSuccess: (run) => {
      invalidate();
      reportUpsRun(run);
    },
  });
}

/** Sets a variable; its dialog shows the failure, so it skips the global toast. */
export function useSetUpsVariable(upsId: string) {
  const invalidate = useInvalidateRuns(upsId);
  return useMutation({
    mutationFn: ({ name, value, confirm }: { name: string; value: string; confirm?: string }) =>
      apiClient.put<UpsCommandRun>(apiPath`/api/v1/ups-devices/${upsId}/vars/${name}`, { value, confirm }),
    onSuccess: invalidate,
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
