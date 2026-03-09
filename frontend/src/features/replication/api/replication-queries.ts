import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

export interface ReplicationJob {
  id: string;
  type: string;
  source?: string;
  target: string;
  guest: number;
  schedule?: string;
  rate?: string;
  comment?: string;
  disable?: number;
  error?: string;
  duration?: number;
  fail_count?: number;
  last_sync?: number;
  last_try?: number;
  next_sync?: number;
  [key: string]: unknown;
}

export function useReplicationJobs(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "replication"],
    queryFn: () => apiClient.get<ReplicationJob[]>(`/api/v1/clusters/${clusterId}/replication`),
    enabled: clusterId.length > 0,
  });
}

export function useCreateReplicationJob(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { id: string; type: string; target: string; schedule?: string; rate?: string; comment?: string }) =>
      apiClient.post(`/api/v1/clusters/${clusterId}/replication`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "replication"] }); },
  });
}

export function useUpdateReplicationJob(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string; schedule?: string; rate?: string; comment?: string; disable?: number }) =>
      apiClient.put(`/api/v1/clusters/${clusterId}/replication/${encodeURIComponent(id)}`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "replication"] }); },
  });
}

export function useDeleteReplicationJob(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/replication/${encodeURIComponent(id)}`),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "replication"] }); },
  });
}

export function useTriggerReplication(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, node }: { id: string; node: string }) =>
      apiClient.post<{ upid: string }>(`/api/v1/clusters/${clusterId}/replication/${encodeURIComponent(id)}/trigger?node=${encodeURIComponent(node)}`),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "replication"] }); },
  });
}
