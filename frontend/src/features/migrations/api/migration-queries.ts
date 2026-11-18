import { useMemo } from "react";
import {
  useQueries,
  useQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { errorsHandledLocally } from "@/hooks/useSaveOutcome";
import type { VMResponse } from "@/types/api";
import type {
  MigrationJob,
  CreateMigrationRequest,
  PreFlightReport,
} from "../types/migration";

export function useMigrationJobs(limit = 50, offset = 0) {
  return useQuery({
    queryKey: ["migrations", limit, offset],
    queryFn: () =>
      apiClient.list<MigrationJob>(
        apiPath`/api/v1/migrations?limit=${limit}&offset=${offset}`,
      ),
  });
}

export function useMigrationJob(id: string) {
  return useQuery({
    queryKey: ["migrations", id],
    queryFn: () =>
      apiClient.get<MigrationJob>(apiPath`/api/v1/migrations/${id}`),
    enabled: id.length > 0,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      // Poll for any non-terminal status (pending, checking, migrating).
      if (
        status === "completed" ||
        status === "failed" ||
        status === "cancelled"
      ) {
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
      apiClient.list<MigrationJob>(
        apiPath`/api/v1/clusters/${clusterId}/migrations?limit=${limit}&offset=${offset}`,
      ),
    enabled: clusterId.length > 0,
  });
}

/**
 * `errorsShownByCaller`: the caller shows a failure itself and settles it
 * through useSaveOutcome, so the app's error toast stays out of it. Left
 * false, the toast reports it.
 */
export function useCreateMigration(errorsShownByCaller = false) {
  const queryClient = useQueryClient();
  return useMutation({
    ...(errorsShownByCaller ? errorsHandledLocally : {}),
    mutationFn: (req: CreateMigrationRequest) =>
      apiClient.post<MigrationJob>(apiPath`/api/v1/migrations`, req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["migrations"] });
    },
  });
}

/**
 * `errorsShownByCaller`: the caller shows a failure itself and settles it
 * through useSaveOutcome, so the app's error toast stays out of it. Left
 * false, the toast reports it.
 */
export function useRunPreFlightCheck(errorsShownByCaller = false) {
  const queryClient = useQueryClient();
  return useMutation({
    ...(errorsShownByCaller ? errorsHandledLocally : {}),
    mutationFn: (id: string) =>
      apiClient.post<PreFlightReport>(apiPath`/api/v1/migrations/${id}/check`),
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
        apiPath`/api/v1/migrations/${id}/execute`,
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
        apiPath`/api/v1/migrations/${id}/cancel`,
      ),
    onSuccess: (_data, id) => {
      void queryClient.invalidateQueries({ queryKey: ["migrations", id] });
      void queryClient.invalidateQueries({ queryKey: ["migrations"] });
    },
  });
}

/**
 * Each job's guest name, keyed `${source_cluster_id}:${vmid}`. A job keeps only
 * the VMID; the name comes from its cluster's guest list, read on the key the
 * tree and the inventory already fill (so mostly from cache). A guest that is
 * gone has no name, and the page shows the VMID alone.
 */
export function useMigrationGuestNames(
  jobs: MigrationJob[] | undefined,
): Map<string, string> {
  const clusterIds = useMemo(
    () => [...new Set((jobs ?? []).map((j) => j.source_cluster_id))],
    [jobs],
  );
  return useQueries({
    queries: clusterIds.map((id) => ({
      queryKey: ["clusters", id, "vms"],
      queryFn: () =>
        apiClient.list<VMResponse>(apiPath`/api/v1/clusters/${id}/vms`),
    })),
    combine: guestNames,
  });
}

// Module-level, so useQueries runs it only when a list changes.
function guestNames(
  lists: { data?: VMResponse[] | undefined }[],
): Map<string, string> {
  const names = new Map<string, string>();
  for (const list of lists) {
    for (const vm of list.data ?? []) {
      if (vm.name) names.set(`${vm.cluster_id}:${String(vm.vmid)}`, vm.name);
    }
  }
  return names;
}
