import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { SortDirection } from "@/hooks/useTableSort";
import type { TaskSortKey } from "../lib/task-columns";

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
  source: string;
  progress: number | null;
  /** Guest VMID parsed from the UPID at insert; absent for non-guest tasks. */
  vmid?: number;
  started_at: string;
  finished_at: string | null;
}

/** Server-side bound on the ?vmids= list (maxVmidsFilter in
 * internal/api/handlers/tasks.go) — requests above it are rejected with 400,
 * so callers must not fire them. */
export const MAX_TASK_VMIDS_FILTER = 500;

interface TaskListParams {
  limit: number;
  offset: number;
  clusterId?: string | undefined;
  status?: string | undefined;
  /** Server-side ordering. See TaskSortKey in ../lib/task-columns — the server
   * rejects anything off its whitelist with a 400, so these are not free-form.
   * Sorting is server-side because the table pages 50 rows out of a history
   * that runs to thousands; ordering the delivered page would only reshuffle
   * what is already on screen. */
  sort: TaskSortKey;
  order: SortDirection;
  /** Server-side guest filter (task_history.vmid). Never pass an empty array —
   * the server treats an absent param as "no filter"; gate with `enabled`. */
  vmids?: number[] | undefined;
  enabled?: boolean | undefined;
}

/**
 * useTasks backs the Tasks page. Server-side cluster + status + vmids
 * filtering with offset pagination (mirrors useAuditLog). The queryKey is
 * prefixed with "tasks" so a WS task_created/task_update event can invalidate
 * every page/filter combination at once (see useEventInvalidation).
 */
export function useTasks({
  limit,
  offset,
  clusterId,
  status,
  sort,
  order,
  vmids,
  enabled,
}: TaskListParams) {
  // Sorted so semantically-equal sets share a cache entry.
  const vmidsKey =
    vmids && vmids.length > 0 ? [...vmids].sort((a, b) => a - b).join(",") : "";

  const params = new URLSearchParams();
  params.set("limit", String(limit));
  params.set("offset", String(offset));
  if (clusterId) params.set("cluster_id", clusterId);
  if (status) params.set("status", status);
  if (vmidsKey) params.set("vmids", vmidsKey);
  params.set("sort", sort);
  params.set("order", order);

  return useQuery({
    queryKey: [
      "tasks",
      limit,
      offset,
      clusterId,
      status,
      sort,
      order,
      vmidsKey,
    ],
    queryFn: () =>
      apiClient.page<TaskRecord>(`/api/v1/tasks?${params.toString()}`),
    enabled: enabled ?? true,
    // Keep the previous page's rows (and, crucially, its `total`) visible
    // while the next page loads — consumers derive page counts from `total`,
    // which would otherwise collapse to 0 during every page transition.
    placeholderData: (prev) => prev,
    refetchInterval: 60_000, // WS events invalidate immediately; this is a fallback
  });
}
