import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  MigrationJob,
  CreateMigrationRequest,
  PreFlightReport,
} from "../types/migration";

export function useMigrationJobs(limit = 50, offset = 0) {
  return useQuery({
    queryKey: ["migrations", limit, offset],
    queryFn: () =>
      apiClient.get<MigrationJob[]>(
        `/api/v1/migrations?limit=${String(limit)}&offset=${String(offset)}`,
      ),
  });
}

export function useMigrationJob(id: string) {
  return useQuery({
    queryKey: ["migrations", id],
    queryFn: () =>
      apiClient.get<MigrationJob>(`/api/v1/migrations/${id}`),
    enabled: id.length > 0,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      // Poll for any non-terminal status (pending, checking, migrating).
      if (status === "completed" || status === "failed" || status === "cancelled") {
        return false;
      }
      return 3000;
    },
  });
}

export function useMigrationJobsByCluster(
  clusterId: string,
  limit = 50,
  offset = 0,
) {
  return useQuery({
    queryKey: ["migrations", "cluster", clusterId, limit, offset],
    queryFn: () =>
      apiClient.get<MigrationJob[]>(
        `/api/v1/clusters/${clusterId}/migrations?limit=${String(limit)}&offset=${String(offset)}`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateMigration() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateMigrationRequest) =>
      apiClient.post<MigrationJob>("/api/v1/migrations", req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["migrations"] });
    },
  });
}

export function useRunPreFlightCheck() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post<PreFlightReport>(`/api/v1/migrations/${id}/check`),
    onSuccess: (_data, id) => {
      void queryClient.invalidateQueries({ queryKey: ["migrations", id] });
    },
  });
}

export function useExecuteMigration() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post<{ status: string; job_id: string; message: string }>(
        `/api/v1/migrations/${id}/execute`,
      ),
    onSuccess: (_data, id) => {
      void queryClient.invalidateQueries({ queryKey: ["migrations", id] });
      void queryClient.invalidateQueries({ queryKey: ["migrations"] });
    },
  });
}

export function useCancelMigration() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post<{ status: string }>(
        `/api/v1/migrations/${id}/cancel`,
      ),
    onSuccess: (_data, id) => {
      void queryClient.invalidateQueries({ queryKey: ["migrations", id] });
      void queryClient.invalidateQueries({ queryKey: ["migrations"] });
    },
  });
}
