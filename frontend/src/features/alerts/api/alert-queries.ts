import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { errorsHandledLocally } from "@/hooks/useSaveOutcome";
import type {
  AlertRule,
  AlertInstance,
  AlertSummary,
  AlertRuleRequest,
  NotificationChannel,
  MaintenanceWindow,
  TestChannelResponse,
  NotificationDLQEntry,
  NotificationDLQSummary,
} from "@/types/api";
import { useRefreshInterval } from "@/lib/refresh-interval";

// --- Alert Rules ---

export function useAlertRules(clusterId?: string) {
  const params = new URLSearchParams();
  if (clusterId) params.set("cluster_id", clusterId);

  return useQuery({
    queryKey: ["alert-rules", clusterId ?? "all"],
    queryFn: () =>
      apiClient.list<AlertRule>(apiPath`/api/v1/alert-rules?${params}`),
  });
}

export function useCreateAlertRule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: AlertRuleRequest) =>
      apiClient.post<AlertRule>(apiPath`/api/v1/alert-rules`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["alert-rules"] });
    },
  });
}

export function useUpdateAlertRule() {
  const qc = useQueryClient();
  return useMutation({
    // The backend merges the PUT body field by field: omitted fields keep
    // their stored values. A present numeric/boolean zero IS applied (a
    // placeholder threshold: 0 would zero the rule's real threshold), while
    // empty strings are treated as absent, not as clears. Send only the
    // fields actually being changed.
    mutationFn: ({ id, ...data }: Partial<AlertRuleRequest> & { id: string }) =>
      apiClient.put<AlertRule>(apiPath`/api/v1/alert-rules/${id}`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["alert-rules"] });
    },
  });
}

export function useDeleteAlertRule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(apiPath`/api/v1/alert-rules/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["alert-rules"] });
    },
  });
}

// --- Alert History ---

export function useAlerts(filters?: {
  state?: string | undefined;
  severity?: string | undefined;
  clusterId?: string | undefined;
}) {
  const params = new URLSearchParams();
  if (filters?.state) params.set("state", filters.state);
  if (filters?.severity) params.set("severity", filters.severity);
  if (filters?.clusterId) params.set("cluster_id", filters.clusterId);

  return useQuery({
    queryKey: ["alerts", filters?.state, filters?.severity, filters?.clusterId],
    queryFn: () =>
      apiClient.list<AlertInstance>(apiPath`/api/v1/alerts?${params}`),
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data) return false;
      const hasActive = data.some(
        (a) => a.state === "firing" || a.state === "pending",
      );
      return hasActive ? 10000 : false;
    },
  });
}

export function useAlertSummary() {
  const refetchInterval = useRefreshInterval();
  return useQuery({
    queryKey: ["alert-summary"],
    queryFn: () => apiClient.get<AlertSummary>(apiPath`/api/v1/alerts/summary`),
    refetchInterval,
  });
}

export function useAcknowledgeAlert() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post(apiPath`/api/v1/alerts/${id}/acknowledge`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["alerts"] });
      void qc.invalidateQueries({ queryKey: ["alert-summary"] });
    },
  });
}

export function useResolveAlert() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post(apiPath`/api/v1/alerts/${id}/resolve`),
    // Settled, not only succeeded: a resolve that lost the race to the engine
    // answers 409, and the row must then show the state the alert is in.
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["alerts"] });
      void qc.invalidateQueries({ queryKey: ["alert-summary"] });
    },
  });
}

// --- Notification Channels ---

export function useNotificationChannels() {
  return useQuery({
    queryKey: ["notification-channels"],
    queryFn: () =>
      apiClient.list<NotificationChannel>(
        apiPath`/api/v1/notification-channels`,
      ),
  });
}

/**
 * Its failures are not toasted: the add dialog shows them, a target on a
 * private network as a prompt, and toasts one that settles after it closed
 * (useSaveOutcome).
 */
export function useCreateNotificationChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      name: string;
      channel_type: string;
      config: Record<string, unknown>;
      enabled?: boolean;
      /** Confirms a target on a private network address. */
      allow_private_address?: boolean;
    }) =>
      apiClient.post<NotificationChannel>(
        apiPath`/api/v1/notification-channels`,
        data,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["notification-channels"] });
    },
    onError: () => undefined,
  });
}

export function useUpdateNotificationChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...data
    }: {
      id: string;
      name?: string;
      channel_type?: string;
      config?: Record<string, unknown>;
      enabled?: boolean;
    }) =>
      apiClient.put<NotificationChannel>(
        apiPath`/api/v1/notification-channels/${id}`,
        data,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["notification-channels"] });
    },
  });
}

export function useDeleteNotificationChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(apiPath`/api/v1/notification-channels/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["notification-channels"] });
    },
  });
}

export function useTestNotificationChannel() {
  return useMutation({
    ...errorsHandledLocally,
    mutationFn: (id: string) =>
      apiClient.post<TestChannelResponse>(
        apiPath`/api/v1/notification-channels/${id}/test`,
      ),
  });
}

// --- Maintenance Windows ---

export function useMaintenanceWindows(clusterId: string) {
  return useQuery({
    queryKey: ["maintenance-windows", clusterId],
    queryFn: () =>
      apiClient.list<MaintenanceWindow>(
        apiPath`/api/v1/clusters/${clusterId}/maintenance-windows`,
      ),
    enabled: !!clusterId,
  });
}

export function useCreateMaintenanceWindow() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      ...data
    }: {
      clusterId: string;
      description: string;
      starts_at: string;
      ends_at: string;
      node_id?: string;
    }) =>
      apiClient.post<MaintenanceWindow>(
        apiPath`/api/v1/clusters/${clusterId}/maintenance-windows`,
        data,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["maintenance-windows", vars.clusterId],
      });
    },
  });
}

export function useDeleteMaintenanceWindow() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      windowId,
    }: {
      clusterId: string;
      windowId: string;
    }) =>
      apiClient.delete(
        apiPath`/api/v1/clusters/${clusterId}/maintenance-windows/${windowId}`,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["maintenance-windows", vars.clusterId],
      });
    },
  });
}

// --- Notification Dead-Letter Queue ---

export function useNotificationDLQ(state?: string) {
  const params = new URLSearchParams();
  if (state) params.set("state", state);

  const refetchInterval = useRefreshInterval();
  return useQuery({
    queryKey: ["notification-dlq", state ?? "all"],
    queryFn: () =>
      apiClient.list<NotificationDLQEntry>(
        apiPath`/api/v1/notification-dlq?${params}`,
      ),
    refetchInterval,
  });
}

export function useNotificationDLQSummary() {
  const refetchInterval = useRefreshInterval();
  return useQuery({
    queryKey: ["notification-dlq-summary"],
    queryFn: () =>
      apiClient.get<NotificationDLQSummary>(
        apiPath`/api/v1/notification-dlq/summary`,
      ),
    refetchInterval,
  });
}

export function useRetryNotificationDLQ() {
  const qc = useQueryClient();
  return useMutation({
    ...errorsHandledLocally,
    mutationFn: (id: string) =>
      apiClient.post<{ success: boolean; message: string }>(
        apiPath`/api/v1/notification-dlq/${id}/retry`,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["notification-dlq"] });
      void qc.invalidateQueries({ queryKey: ["notification-dlq-summary"] });
    },
  });
}

export function useDismissNotificationDLQ() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post<{ success: boolean }>(
        apiPath`/api/v1/notification-dlq/${id}/dismiss`,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["notification-dlq"] });
      void qc.invalidateQueries({ queryKey: ["notification-dlq-summary"] });
    },
  });
}

export function useDeleteNotificationDLQ() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(apiPath`/api/v1/notification-dlq/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["notification-dlq"] });
      void qc.invalidateQueries({ queryKey: ["notification-dlq-summary"] });
    },
  });
}
