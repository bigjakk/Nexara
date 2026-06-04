import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

/** A row from the reconciled task_history table (server-authoritative status). */
export interface TaskRecord {
  id: string;
  cluster_id: string;
  upid: string;
  description: string;
  status: string;
  exit_status: string;
  node: string;
  task_type: string;
  progress: number | null;
  started_at: string;
  finished_at: string | null;
}

export interface TaskListResponse {
  items: TaskRecord[];
  total: number;
}

interface TaskListParams {
  limit: number;
  offset: number;
  clusterId?: string | undefined;
  status?: string | undefined;
}

/**
 * useTasks backs the Tasks page. Server-side cluster + status filtering with
 * offset pagination (mirrors useAuditLog). The queryKey is prefixed with
 * "tasks" so a WS task_created/task_update event can invalidate every page/
 * filter combination at once (see useEventInvalidation).
 */
export function useTasks({ limit, offset, clusterId, status }: TaskListParams) {
  const params = new URLSearchParams();
  params.set("limit", String(limit));
  params.set("offset", String(offset));
  if (clusterId) params.set("cluster_id", clusterId);
  if (status) params.set("status", status);

  return useQuery({
    queryKey: ["tasks", limit, offset, clusterId, status],
    queryFn: () =>
      apiClient.get<TaskListResponse>(`/api/v1/tasks?${params.toString()}`),
    refetchInterval: 60_000, // WS events invalidate immediately; this is a fallback
  });
}
