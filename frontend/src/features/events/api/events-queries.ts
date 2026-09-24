import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath, type ApiPath } from "@/lib/api-path";
import { usePermissions } from "@/hooks/usePermissions";
import type { AuditLogEntry } from "@/features/audit/api/audit-queries";

// Re-export the canonical audit entry type (owned by the audit feature) so
// Events-tab consumers such as AuditLogPanel share one shape — including the
// task_status/task_exit_status/task_progress fields the /api/v1/audit-log
// endpoint returns — instead of a local copy that silently drifted and
// omitted them. The response envelope itself is ListResponse<AuditLogEntry>,
// which every collection endpoint shares (@/lib/api-client).
export type { AuditLogEntry };

export interface AuditUserRef {
  id: string;
  email: string;
  display_name: string;
}

interface EventsParams {
  limit: number;
  offset: number;
  clusterId?: string | undefined;
  resourceType?: string | undefined;
  userId?: string | undefined;
  action?: string | undefined;
  source?: string | undefined;
  startTime?: string | undefined;
  endTime?: string | undefined;
}

export function useEvents({
  limit,
  offset,
  clusterId,
  resourceType,
  userId,
  action,
  source,
  startTime,
  endTime,
}: EventsParams) {
  const params = new URLSearchParams();
  params.set("limit", String(limit));
  params.set("offset", String(offset));
  if (clusterId) params.set("cluster_id", clusterId);
  if (resourceType) params.set("resource_type", resourceType);
  if (userId) params.set("user_id", userId);
  if (action) params.set("action", action);
  if (source) params.set("source", source);
  if (startTime) params.set("start_time", startTime);
  if (endTime) params.set("end_time", endTime);

  return useQuery({
    queryKey: [
      "audit-log",
      limit,
      offset,
      clusterId,
      resourceType,
      userId,
      action,
      source,
      startTime,
      endTime,
    ],
    queryFn: () =>
      apiClient.page<AuditLogEntry>(apiPath`/api/v1/audit-log?${params}`),
    refetchInterval: 120_000,
  });
}

export function useAuditActions() {
  return useQuery({
    queryKey: ["audit-actions"],
    queryFn: () => apiClient.list<string>(apiPath`/api/v1/audit-log/actions`),
    staleTime: 300_000,
  });
}

export function useAuditUsers() {
  return useQuery({
    queryKey: ["audit-users"],
    queryFn: () =>
      apiClient.list<AuditUserRef>(apiPath`/api/v1/audit-log/users`),
    staleTime: 300_000,
  });
}

// --- Syslog forwarding config ---

export interface SyslogConfig {
  enabled: boolean;
  host: string;
  port: number;
  protocol: string;
  facility: number;
  tls_skip_verify: boolean;
}

/**
 * What a 200 from PUT /audit-log/syslog-config carries in place of the stored
 * config when the config was stored but the live forwarder could not connect
 * to the collector it names (UpdateSyslogConfig, internal/api/handlers/audit.go).
 */
export interface SyslogSaveWarning {
  saved: true;
  warning: string;
}

export function useSyslogConfig() {
  // The syslog routes all require a global manage:audit (registry_audit.go).
  // Without it this read can only 403, so it is not sent at all. canManage
  // reads the flat permission list, which carries cluster-scoped grants too
  // (GetUserPermissions in queries/rbac.sql ignores scope), so a holder of
  // manage:audit on one cluster only still sends it, and meets the card's
  // could-not-load notice.
  const { canManage } = usePermissions();
  return useQuery({
    queryKey: ["syslog-config"],
    queryFn: () =>
      apiClient.get<SyslogConfig>(apiPath`/api/v1/audit-log/syslog-config`),
    staleTime: 60_000,
    enabled: canManage("audit"),
  });
}

export function useSaveSyslogConfig() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (cfg: SyslogConfig) =>
      apiClient.put<SyslogConfig | SyslogSaveWarning>(
        apiPath`/api/v1/audit-log/syslog-config`,
        cfg,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["syslog-config"] });
    },
  });
}

export function useTestSyslog() {
  return useMutation({
    mutationFn: (cfg: SyslogConfig) =>
      apiClient.post<{ success: boolean; error?: string }>(
        apiPath`/api/v1/audit-log/syslog-test`,
        cfg,
      ),
  });
}

export function buildExportUrl(
  format: "json" | "csv" | "syslog",
  filters: {
    clusterId?: string | undefined;
    resourceType?: string | undefined;
    userId?: string | undefined;
    action?: string | undefined;
    startTime?: string | undefined;
    endTime?: string | undefined;
  },
): ApiPath {
  const params = new URLSearchParams();
  params.set("format", format);
  if (filters.clusterId) params.set("cluster_id", filters.clusterId);
  if (filters.resourceType) params.set("resource_type", filters.resourceType);
  if (filters.userId) params.set("user_id", filters.userId);
  if (filters.action) params.set("action", filters.action);
  if (filters.startTime) params.set("start_time", filters.startTime);
  if (filters.endTime) params.set("end_time", filters.endTime);
  return apiPath`/api/v1/audit-log/export?${params}`;
}
