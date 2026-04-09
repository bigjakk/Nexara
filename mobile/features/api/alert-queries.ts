import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiGet, apiPost } from "./api-client";
import { queryKeys } from "./query-keys";
import type { Alert, AlertListFilters, AlertSummary } from "./types";

function buildAlertPath(filters?: AlertListFilters): string {
  if (!filters) return "/alerts";
  const params = new URLSearchParams();
  if (filters.state) params.set("state", filters.state);
  if (filters.severity) params.set("severity", filters.severity);
  if (filters.cluster_id) params.set("cluster_id", filters.cluster_id);
  if (typeof filters.limit === "number")
    params.set("limit", String(filters.limit));
  if (typeof filters.offset === "number")
    params.set("offset", String(filters.offset));
  const qs = params.toString();
  return qs ? `/alerts?${qs}` : "/alerts";
}

export function useAlerts(filters?: AlertListFilters) {
  return useQuery({
    queryKey: queryKeys.alerts(filters as Record<string, unknown> | undefined),
    queryFn: () => apiGet<Alert[]>(buildAlertPath(filters)),
    staleTime: 10_000,
  });
}

export function useAlertSummary() {
  return useQuery({
    queryKey: queryKeys.alertSummary(),
    queryFn: () => apiGet<AlertSummary>("/alerts/summary"),
    staleTime: 10_000,
  });
}

export function useAcknowledgeAlert() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (alertId: string) =>
      apiPost<{ status: string }>(`/alerts/${alertId}/acknowledge`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.alerts() });
      void qc.invalidateQueries({ queryKey: queryKeys.alertSummary() });
    },
  });
}

export function useResolveAlert() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (alertId: string) =>
      apiPost<{ status: string }>(`/alerts/${alertId}/resolve`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.alerts() });
      void qc.invalidateQueries({ queryKey: queryKeys.alertSummary() });
    },
  });
}
