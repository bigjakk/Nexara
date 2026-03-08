import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  AlertRule,
  AlertInstance,
  AlertSummary,
  AlertRuleRequest,
  NotificationChannel,
  MaintenanceWindow,
} from "@/types/api";

// --- Alert Rules ---

export function useAlertRules(clusterId?: string) {
  const params = new URLSearchParams();
  if (clusterId) params.set("cluster_id", clusterId);
  const qs = params.toString();

  return useQuery({
    queryKey: ["alert-rules", clusterId ?? "all"],
    queryFn: () =>
      apiClient.get<AlertRule[]>(`/api/v1/alert-rules${qs ? `?${qs}` : ""}`),
  });
}

export function useCreateAlertRule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: AlertRuleRequest) =>
      apiClient.post<AlertRule>("/api/v1/alert-rules", data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["alert-rules"] });
    },
  });
}

export function useUpdateAlertRule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...data }: AlertRuleRequest & { id: string }) =>
      apiClient.put<AlertRule>(`/api/v1/alert-rules/${id}`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["alert-rules"] });
    },
  });
}

export function useDeleteAlertRule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.delete(`/api/v1/alert-rules/${id}`),
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
  const qs = params.toString();

  return useQuery({
    queryKey: ["alerts", filters?.state, filters?.severity, filters?.clusterId],
    queryFn: () =>
      apiClient.get<AlertInstance[]>(`/api/v1/alerts${qs ? `?${qs}` : ""}`),
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
  return useQuery({
    queryKey: ["alert-summary"],
    queryFn: () => apiClient.get<AlertSummary>("/api/v1/alerts/summary"),
    refetchInterval: 30000,
  });
}

export function useAcknowledgeAlert() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post(`/api/v1/alerts/${id}/acknowledge`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["alerts"] });
      void qc.invalidateQueries({ queryKey: ["alert-summary"] });
    },
  });
}

export function useResolveAlert() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.post(`/api/v1/alerts/${id}/resolve`),
    onSuccess: () => {
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
      apiClient.get<NotificationChannel[]>("/api/v1/notification-channels"),
  });
}

export function useCreateNotificationChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      name: string;
      channel_type: string;
      config: Record<string, unknown>;
      enabled?: boolean;
    }) => apiClient.post<NotificationChannel>("/api/v1/notification-channels", data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["notification-channels"] });
    },
  });
}

export function useDeleteNotificationChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(`/api/v1/notification-channels/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["notification-channels"] });
    },
  });
}

// --- Maintenance Windows ---

export function useMaintenanceWindows(clusterId: string) {
  return useQuery({
    queryKey: ["maintenance-windows", clusterId],
    queryFn: () =>
      apiClient.get<MaintenanceWindow[]>(
        `/api/v1/clusters/${clusterId}/maintenance-windows`,
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
        `/api/v1/clusters/${clusterId}/maintenance-windows`,
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
        `/api/v1/clusters/${clusterId}/maintenance-windows/${windowId}`,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["maintenance-windows", vars.clusterId],
      });
    },
  });
}
