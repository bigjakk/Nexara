import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

export interface AuditLogEntry {
  id: string;
  cluster_id: string | null;
  user_id: string;
  resource_type: string;
  resource_id: string;
  action: string;
  details: string;
  created_at: string;
  user_email: string;
  user_display_name: string;
  cluster_name: string;
  resource_vmid: number;
  resource_name: string;
}

export interface AuditLogResponse {
  items: AuditLogEntry[];
  total: number;
}

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
      startTime,
      endTime,
    ],
    queryFn: () =>
      apiClient.get<AuditLogResponse>(
        `/api/v1/audit-log?${params.toString()}`,
      ),
    refetchInterval: 120_000,
  });
}

export function useAuditActions() {
  return useQuery({
    queryKey: ["audit-actions"],
    queryFn: () => apiClient.get<string[]>("/api/v1/audit-log/actions"),
    staleTime: 300_000,
  });
}

export function useAuditUsers() {
  return useQuery({
    queryKey: ["audit-users"],
    queryFn: () =>
      apiClient.get<AuditUserRef[]>("/api/v1/audit-log/users"),
    staleTime: 300_000,
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
): string {
  const params = new URLSearchParams();
  params.set("format", format);
  if (filters.clusterId) params.set("cluster_id", filters.clusterId);
  if (filters.resourceType)
    params.set("resource_type", filters.resourceType);
  if (filters.userId) params.set("user_id", filters.userId);
  if (filters.action) params.set("action", filters.action);
  if (filters.startTime) params.set("start_time", filters.startTime);
  if (filters.endTime) params.set("end_time", filters.endTime);
  return `/api/v1/audit-log/export?${params.toString()}`;
}
