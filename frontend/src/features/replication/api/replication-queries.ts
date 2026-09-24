import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";

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
    queryFn: () =>
      apiClient.list<ReplicationJob>(
        apiPath`/api/v1/clusters/${clusterId}/replication`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateReplicationJob(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      id: string;
      type: string;
      target: string;
      schedule?: string;
      rate?: string;
      comment?: string;
    }) =>
      apiClient.post(apiPath`/api/v1/clusters/${clusterId}/replication`, data),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: ["clusters", clusterId, "replication"],
      });
    },
  });
}

export function useUpdateReplicationJob(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...data
    }: {
      id: string;
      schedule?: string;
      rate?: string;
      comment?: string;
      disable?: number;
    }) =>
      apiClient.put(
        apiPath`/api/v1/clusters/${clusterId}/replication/${id}`,
        data,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: ["clusters", clusterId, "replication"],
      });
    },
  });
}

export function useDeleteReplicationJob(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(
        apiPath`/api/v1/clusters/${clusterId}/replication/${id}`,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: ["clusters", clusterId, "replication"],
      });
    },
  });
}

export function useTriggerReplication(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, node }: { id: string; node: string }) =>
      apiClient.post<{ upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/replication/${id}/trigger?node=${node}`,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: ["clusters", clusterId, "replication"],
      });
    },
  });
}
