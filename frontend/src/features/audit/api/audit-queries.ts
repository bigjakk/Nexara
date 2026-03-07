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

interface AuditLogParams {
  limit: number;
  offset: number;
  clusterId?: string | undefined;
  resourceType?: string | undefined;
}

export function useAuditLog({ limit, offset, clusterId, resourceType }: AuditLogParams) {
  const params = new URLSearchParams();
  params.set("limit", String(limit));
  params.set("offset", String(offset));
  if (clusterId) params.set("cluster_id", clusterId);
  if (resourceType) params.set("resource_type", resourceType);

  return useQuery({
    queryKey: ["audit-log", limit, offset, clusterId, resourceType],
    queryFn: () =>
      apiClient.get<AuditLogResponse>(
        `/api/v1/audit-log?${params.toString()}`,
      ),
    refetchInterval: 120_000, // WS events handle immediate updates
  });
}
