import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";

export interface MetricServerConfig {
  id: string;
  type: string;
  server: string;
  port: number;
  disable?: number;
  proto?: string;
  influxdbproto?: string;
  organization?: string;
  bucket?: string;
  [key: string]: unknown;
}

export function useMetricServers(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "metric-servers"],
    queryFn: () =>
      apiClient.list<MetricServerConfig>(
        apiPath`/api/v1/clusters/${clusterId}/metric-servers`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateMetricServer(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      id: string;
      type: string;
      server: string;
      port: number;
      [key: string]: unknown;
    }) =>
      apiClient.post(
        apiPath`/api/v1/clusters/${clusterId}/metric-servers`,
        data,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: ["clusters", clusterId, "metric-servers"],
      });
    },
  });
}

export function useUpdateMetricServer(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string; [key: string]: unknown }) =>
      apiClient.put(
        apiPath`/api/v1/clusters/${clusterId}/metric-servers/${id}`,
        data,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: ["clusters", clusterId, "metric-servers"],
      });
    },
  });
}

export function useDeleteMetricServer(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(
        apiPath`/api/v1/clusters/${clusterId}/metric-servers/${id}`,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: ["clusters", clusterId, "metric-servers"],
      });
    },
  });
}
