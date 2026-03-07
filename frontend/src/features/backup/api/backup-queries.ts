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
  PBSTaskLogEntry,
  RestoreRequest,
  DeleteSnapshotRequest,
  ProtectSnapshotRequest,
  UpdateSnapshotNotesRequest,
  PBSPruneRequest,
  PBSPruneResult,
  BackupJob,
  BackupJobParams,
  TriggerBackupRequest,
  PBSDatastoreRRDEntry,
  PBSDatastoreConfig,
  BackupCoverageEntry,
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
    refetchInterval: 120_000,
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

// --- Snapshot by Backup ID (cross-server, for VM detail pages) ---

export function usePBSSnapshotsByBackupID(backupId: string) {
  return useQuery({
    queryKey: ["pbs-snapshots", backupId],
    queryFn: () =>
      apiClient.get<PBSSnapshot[]>(
        `/api/v1/pbs-snapshots?backup_id=${encodeURIComponent(backupId)}`,
      ),
    enabled: backupId.length > 0,
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
    refetchInterval: 120_000, // WS pbs_change events handle real-time; this is a fallback
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
    refetchInterval: timeframe === "latest" ? 120_000 : false,
  });
}

// --- Datastore RRD (live proxy) ---

export function usePBSDatastoreRRD(
  pbsId: string,
  store: string,
  timeframe: string = "hour",
  cf: string = "AVERAGE",
) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "datastores", store, "rrd", timeframe, cf],
    queryFn: () =>
      apiClient.get<PBSDatastoreRRDEntry[]>(
        `/api/v1/pbs-servers/${pbsId}/datastores/${encodeURIComponent(store)}/rrd?timeframe=${timeframe}&cf=${cf}`,
      ),
    enabled: pbsId.length > 0 && store.length > 0,
    refetchInterval: 120_000,
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
      body,
    }: {
      pbsId: string;
      store: string;
      body: DeleteSnapshotRequest;
    }) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/pbs-servers/${pbsId}/datastores/${encodeURIComponent(store)}/snapshots`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["pbs-servers", variables.pbsId, "snapshots"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["pbs-servers", variables.pbsId, "tasks"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["pbs-snapshots"],
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

interface UpdatePBSServerRequest {
  name?: string;
  api_url?: string;
  token_id?: string;
  token_secret?: string;
  tls_fingerprint?: string;
  cluster_id?: string;
}

export function useUpdatePBSServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ id, ...body }: UpdatePBSServerRequest & { id: string }) =>
      apiClient.put<PBSServer>(`/api/v1/pbs-servers/${id}`, body),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["pbs-servers"] });
    },
  });
}

// --- Phase 1: Snapshot Management ---

export function useProtectSnapshot() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      pbsId,
      store,
      body,
    }: {
      pbsId: string;
      store: string;
      body: ProtectSnapshotRequest;
    }) =>
      apiClient.put<{ status: string }>(
        `/api/v1/pbs-servers/${pbsId}/datastores/${encodeURIComponent(store)}/snapshots/protect`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["pbs-servers", variables.pbsId, "snapshots"],
      });
    },
  });
}

export function useUpdateSnapshotNotes() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      pbsId,
      store,
      body,
    }: {
      pbsId: string;
      store: string;
      body: UpdateSnapshotNotesRequest;
    }) =>
      apiClient.put<{ status: string }>(
        `/api/v1/pbs-servers/${pbsId}/datastores/${encodeURIComponent(store)}/snapshots/notes`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["pbs-servers", variables.pbsId, "snapshots"],
      });
    },
  });
}

export function usePBSTaskLog(pbsId: string, upid: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "tasks", upid, "log"],
    queryFn: () =>
      apiClient.get<PBSTaskLogEntry[]>(
        `/api/v1/pbs-servers/${pbsId}/tasks/${encodeURIComponent(upid)}/log`,
      ),
    enabled: pbsId.length > 0 && upid.length > 0,
  });
}

// --- Phase 2: Backup Jobs ---

export function useTriggerBackup() {
  return useMutation({
    mutationFn: ({
      clusterId,
      body,
    }: {
      clusterId: string;
      body: TriggerBackupRequest;
    }) =>
      apiClient.post<{ upid: string }>(
        `/api/v1/clusters/${clusterId}/backup`,
        body,
      ),
  });
}

export function useBackupJobs(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "backup-jobs"],
    queryFn: () =>
      apiClient.get<BackupJob[]>(
        `/api/v1/clusters/${clusterId}/backup-jobs`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateBackupJob() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      clusterId,
      body,
    }: {
      clusterId: string;
      body: BackupJobParams;
    }) =>
      apiClient.post<{ status: string }>(
        `/api/v1/clusters/${clusterId}/backup-jobs`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "backup-jobs"],
      });
    },
  });
}

export function useUpdateBackupJob() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      clusterId,
      jobId,
      body,
    }: {
      clusterId: string;
      jobId: string;
      body: BackupJobParams;
    }) =>
      apiClient.put<{ status: string }>(
        `/api/v1/clusters/${clusterId}/backup-jobs/${encodeURIComponent(jobId)}`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "backup-jobs"],
      });
    },
  });
}

export function useDeleteBackupJob() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      clusterId,
      jobId,
    }: {
      clusterId: string;
      jobId: string;
    }) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/clusters/${clusterId}/backup-jobs/${encodeURIComponent(jobId)}`,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "backup-jobs"],
      });
    },
  });
}

export function useRunBackupJob() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      clusterId,
      jobId,
    }: {
      clusterId: string;
      jobId: string;
    }) =>
      apiClient.post<{ upid: string }>(
        `/api/v1/clusters/${clusterId}/backup-jobs/${encodeURIComponent(jobId)}/run`,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "backup-jobs"],
      });
    },
  });
}

// --- Phase 3: Prune ---

export function usePruneDatastore() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      pbsId,
      store,
      body,
    }: {
      pbsId: string;
      store: string;
      body: PBSPruneRequest;
    }) =>
      apiClient.post<PBSPruneResult[]>(
        `/api/v1/pbs-servers/${pbsId}/datastores/${encodeURIComponent(store)}/prune`,
        body,
      ),
    onSuccess: (_data, variables) => {
      if (!variables.body.dry_run) {
        void queryClient.invalidateQueries({
          queryKey: ["pbs-servers", variables.pbsId, "snapshots"],
        });
        void queryClient.invalidateQueries({
          queryKey: ["pbs-servers", variables.pbsId, "tasks"],
        });
      }
    },
  });
}

// --- Datastore Config ---

export function useDatastoreConfig(pbsId: string, store: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "datastores", store, "config"],
    queryFn: () =>
      apiClient.get<PBSDatastoreConfig>(
        `/api/v1/pbs-servers/${pbsId}/datastores/${encodeURIComponent(store)}/config`,
      ),
    enabled: pbsId.length > 0 && store.length > 0,
    staleTime: 120_000,
  });
}

// --- Backup Coverage ---

export function useBackupCoverage() {
  return useQuery({
    queryKey: ["backup-coverage"],
    queryFn: () =>
      apiClient.get<BackupCoverageEntry[]>("/api/v1/backup-coverage"),
    staleTime: 60_000,
    refetchInterval: 120_000,
  });
}
