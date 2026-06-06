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
  source: string;
  user_email: string;
  user_display_name: string;
  cluster_name: string;
  resource_vmid: number;
  resource_name: string;
  // Server-authoritative task status for UPID-bearing entries (Nexara tasks).
  // Absent for non-task or external entries.
  task_status?: string;
  task_exit_status?: string;
  task_progress?: number;
}

export interface AuditLogResponse {
  items: AuditLogEntry[];
  total: number;
}

export function useRecentActivity() {
  return useQuery({
    queryKey: ["recent-activity"],
    queryFn: () =>
      apiClient.get<AuditLogEntry[]>("/api/v1/audit-log/recent"),
    refetchInterval: 120_000,
  });
}
