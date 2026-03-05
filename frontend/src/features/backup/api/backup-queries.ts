import {
  useQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  PBSServer,
  PBSDatastore,
  PBSDatastoreStatus,
  PBSSnapshot,
  PBSSyncJob,
  PBSVerifyJob,
  PBSTask,
  PBSDatastoreMetric,
  RestoreRequest,
} from "../types/backup";

// --- PBS Server Queries ---

export function usePBSServers() {
  return useQuery({
    queryKey: ["pbs-servers"],
    queryFn: () => apiClient.get<PBSServer[]>("/api/v1/pbs-servers"),
  });
}

// --- Datastore Queries (live proxy) ---

export function usePBSDatastores(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "datastores"],
    queryFn: () =>
      apiClient.get<PBSDatastore[]>(
        `/api/v1/pbs-servers/${pbsId}/datastores`,
      ),
    enabled: pbsId.length > 0,
  });
}

export function usePBSDatastoreStatus(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "datastores", "status"],
    queryFn: () =>
      apiClient.get<PBSDatastoreStatus[]>(
        `/api/v1/pbs-servers/${pbsId}/datastores/status`,
      ),
    enabled: pbsId.length > 0,
    refetchInterval: 30000,
  });
}

// --- Snapshot Queries (DB-backed) ---

export function usePBSSnapshots(pbsId: string, datastore?: string) {
  const params = datastore ? `?datastore=${encodeURIComponent(datastore)}` : "";
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "snapshots", datastore ?? "all"],
    queryFn: () =>
      apiClient.get<PBSSnapshot[]>(
        `/api/v1/pbs-servers/${pbsId}/snapshots${params}`,
      ),
    enabled: pbsId.length > 0,
  });
}

// --- Sync Job Queries (DB-backed) ---

export function usePBSSyncJobs(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "sync-jobs"],
    queryFn: () =>
      apiClient.get<PBSSyncJob[]>(
        `/api/v1/pbs-servers/${pbsId}/sync-jobs`,
      ),
    enabled: pbsId.length > 0,
  });
}

// --- Verify Job Queries (DB-backed) ---

export function usePBSVerifyJobs(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "verify-jobs"],
    queryFn: () =>
      apiClient.get<PBSVerifyJob[]>(
        `/api/v1/pbs-servers/${pbsId}/verify-jobs`,
      ),
    enabled: pbsId.length > 0,
  });
}

// --- Task Queries (live proxy) ---

export function usePBSTasks(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "tasks"],
    queryFn: () =>
      apiClient.get<PBSTask[]>(
        `/api/v1/pbs-servers/${pbsId}/tasks?limit=50`,
      ),
    enabled: pbsId.length > 0,
    refetchInterval: 10000,
  });
}

// --- Metric Queries (DB-backed) ---

export function usePBSDatastoreMetrics(
  pbsId: string,
  timeframe: string = "latest",
) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "metrics", timeframe],
    queryFn: () =>
      apiClient.get<PBSDatastoreMetric[]>(
        `/api/v1/pbs-servers/${pbsId}/metrics?timeframe=${timeframe}`,
      ),
    enabled: pbsId.length > 0,
    refetchInterval: timeframe === "latest" ? 30000 : false,
  });
}

// --- Mutations ---

export function useTriggerGC() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ pbsId, store }: { pbsId: string; store: string }) =>
      apiClient.post<{ upid: string }>(
        `/api/v1/pbs-servers/${pbsId}/datastores/${encodeURIComponent(store)}/gc`,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["pbs-servers", variables.pbsId, "tasks"],
      });
    },
  });
}

export function useDeleteSnapshot() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      pbsId,
      store,
    }: {
      pbsId: string;
      store: string;
    }) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/pbs-servers/${pbsId}/datastores/${encodeURIComponent(store)}/snapshots`,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["pbs-servers", variables.pbsId, "snapshots"],
      });
    },
  });
}

export function useRunSyncJob() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ pbsId, jobId }: { pbsId: string; jobId: string }) =>
      apiClient.post<{ upid: string }>(
        `/api/v1/pbs-servers/${pbsId}/sync-jobs/${encodeURIComponent(jobId)}/run`,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["pbs-servers", variables.pbsId, "tasks"],
      });
    },
  });
}

export function useRunVerifyJob() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ pbsId, jobId }: { pbsId: string; jobId: string }) =>
      apiClient.post<{ upid: string }>(
        `/api/v1/pbs-servers/${pbsId}/verify-jobs/${encodeURIComponent(jobId)}/run`,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["pbs-servers", variables.pbsId, "tasks"],
      });
    },
  });
}

export function useRestoreBackup() {
  return useMutation({
    mutationFn: ({
      clusterId,
      body,
    }: {
      clusterId: string;
      body: RestoreRequest;
    }) =>
      apiClient.post<{ upid: string; status: string }>(
        `/api/v1/clusters/${clusterId}/restore`,
        body,
      ),
  });
}

interface CreatePBSServerRequest {
  name: string;
  api_url: string;
  token_id: string;
  token_secret: string;
  tls_fingerprint: string;
  cluster_id: string | null;
}

export function useCreatePBSServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (req: CreatePBSServerRequest) =>
      apiClient.post<PBSServer>("/api/v1/pbs-servers", req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["pbs-servers"] });
    },
  });
}

export function useDeletePBSServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (pbsId: string) =>
      apiClient.delete<{ status: string }>(`/api/v1/pbs-servers/${pbsId}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["pbs-servers"] });
    },
  });
}
