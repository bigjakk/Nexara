import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath, queryParams } from "@/lib/api-path";
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
  PBSPruneJob,
  BackupCoverageEntry,
  VeeamServer,
  VeeamProbeResult,
  CreateVeeamServerRequest,
  UpdateVeeamServerRequest,
  VeeamRepository,
  VeeamRepositoryMetric,
  VeeamJob,
  VeeamSession,
  VeeamBackupObject,
  VeeamRestorePoint,
  VeeamPlatform,
  VeeamInfrastructureGuest,
  VeeamOrphanedObject,
  VeeamGuestProtection,
  VeeamSessionLogRecord,
  VeeamTaskSession,
} from "../types/backup";

// --- PBS Server Queries ---

export function usePBSServers() {
  return useQuery({
    queryKey: ["pbs-servers"],
    queryFn: () => apiClient.list<PBSServer>(apiPath`/api/v1/pbs-servers`),
  });
}

// --- Datastore Queries (live proxy) ---

export function usePBSDatastores(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "datastores"],
    queryFn: () =>
      apiClient.list<PBSDatastore>(
        apiPath`/api/v1/pbs-servers/${pbsId}/datastores`,
      ),
    enabled: pbsId.length > 0,
  });
}

export function usePBSDatastoreStatus(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "datastores", "status"],
    queryFn: () =>
      apiClient.list<PBSDatastoreStatus>(
        apiPath`/api/v1/pbs-servers/${pbsId}/datastores/status`,
      ),
    enabled: pbsId.length > 0,
    refetchInterval: 120_000,
  });
}

// --- Snapshot Queries (DB-backed) ---

export function usePBSSnapshots(pbsId: string, datastore?: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "snapshots", datastore ?? "all"],
    queryFn: () =>
      apiClient.list<PBSSnapshot>(
        apiPath`/api/v1/pbs-servers/${pbsId}/snapshots?${queryParams({ datastore: datastore || undefined })}`,
      ),
    enabled: pbsId.length > 0,
  });
}

// --- Snapshot by Backup ID (cross-server, for VM detail pages) ---

export function usePBSSnapshotsByBackupID(backupId: string) {
  return useQuery({
    queryKey: ["pbs-snapshots", backupId],
    queryFn: () =>
      apiClient.list<PBSSnapshot>(
        apiPath`/api/v1/pbs-snapshots?backup_id=${backupId}`,
      ),
    enabled: backupId.length > 0,
  });
}

// --- Sync Job Queries (DB-backed) ---

export function usePBSSyncJobs(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "sync-jobs"],
    queryFn: () =>
      apiClient.list<PBSSyncJob>(
        apiPath`/api/v1/pbs-servers/${pbsId}/sync-jobs`,
      ),
    enabled: pbsId.length > 0,
  });
}

// --- Verify Job Queries (DB-backed) ---

export function usePBSVerifyJobs(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "verify-jobs"],
    queryFn: () =>
      apiClient.list<PBSVerifyJob>(
        apiPath`/api/v1/pbs-servers/${pbsId}/verify-jobs`,
      ),
    enabled: pbsId.length > 0,
  });
}

// --- Task Queries (live proxy) ---

export function usePBSTasks(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "tasks"],
    queryFn: () =>
      apiClient.list<PBSTask>(
        apiPath`/api/v1/pbs-servers/${pbsId}/tasks?limit=50`,
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
      apiClient.list<PBSDatastoreMetric>(
        apiPath`/api/v1/pbs-servers/${pbsId}/metrics?timeframe=${timeframe}`,
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
      apiClient.list<PBSDatastoreRRDEntry>(
        apiPath`/api/v1/pbs-servers/${pbsId}/datastores/${store}/rrd?timeframe=${timeframe}&cf=${cf}`,
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
        apiPath`/api/v1/pbs-servers/${pbsId}/datastores/${store}/gc`,
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
        apiPath`/api/v1/pbs-servers/${pbsId}/datastores/${store}/snapshots`,
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
        apiPath`/api/v1/pbs-servers/${pbsId}/sync-jobs/${jobId}/run`,
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
        apiPath`/api/v1/pbs-servers/${pbsId}/verify-jobs/${jobId}/run`,
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
        apiPath`/api/v1/clusters/${clusterId}/restore`,
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
  allow_private_address?: boolean;
}

export function useCreatePBSServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (req: CreatePBSServerRequest) =>
      apiClient.post<PBSServer>(apiPath`/api/v1/pbs-servers`, req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["pbs-servers"] });
    },
  });
}

export function useDeletePBSServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (pbsId: string) =>
      apiClient.delete<{ status: string }>(
        apiPath`/api/v1/pbs-servers/${pbsId}`,
      ),
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
  allow_private_address?: boolean;
}

export function useUpdatePBSServer() {
  const queryClient = useQueryClient();

  return useMutation({
    // `id` is destructured OUT of the payload on purpose, not for tidiness:
    // the endpoint declares it as a PATH parameter, so the server rejects it
    // in the body with "id: must be sent in the request path". The edit
    // dialog builds its object with `id` included, so passing the whole
    // thing through would 400 every PBS edit.
    mutationFn: ({ id, ...body }: UpdatePBSServerRequest & { id: string }) =>
      apiClient.put<PBSServer>(apiPath`/api/v1/pbs-servers/${id}`, body),
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
        apiPath`/api/v1/pbs-servers/${pbsId}/datastores/${store}/snapshots/protect`,
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
        apiPath`/api/v1/pbs-servers/${pbsId}/datastores/${store}/snapshots/notes`,
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
      apiClient.list<PBSTaskLogEntry>(
        apiPath`/api/v1/pbs-servers/${pbsId}/tasks/${upid}/log`,
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
        apiPath`/api/v1/clusters/${clusterId}/backup`,
        body,
      ),
  });
}

export function useBackupJobs(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "backup-jobs"],
    queryFn: () =>
      apiClient.list<BackupJob>(
        apiPath`/api/v1/clusters/${clusterId}/backup-jobs`,
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
        apiPath`/api/v1/clusters/${clusterId}/backup-jobs`,
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
        apiPath`/api/v1/clusters/${clusterId}/backup-jobs/${jobId}`,
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
    mutationFn: ({ clusterId, jobId }: { clusterId: string; jobId: string }) =>
      apiClient.delete<{ status: string }>(
        apiPath`/api/v1/clusters/${clusterId}/backup-jobs/${jobId}`,
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
    mutationFn: ({ clusterId, jobId }: { clusterId: string; jobId: string }) =>
      apiClient.post<{ upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/backup-jobs/${jobId}/run`,
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
        apiPath`/api/v1/pbs-servers/${pbsId}/datastores/${store}/prune`,
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
        apiPath`/api/v1/pbs-servers/${pbsId}/datastores/${store}/config`,
      ),
    enabled: pbsId.length > 0 && store.length > 0,
    staleTime: 120_000,
  });
}

// --- Prune Jobs ---

/**
 * Prune jobs for one datastore.
 *
 * Separate from useDatastoreConfig because PBS keeps prune schedules outside
 * datastore.cfg; reading only the datastore's own config is what made the
 * config card report "Prune Schedule: Not set" on a datastore that prunes
 * daily.
 *
 * One fetch of the whole job list, narrowed with `select`, rather than a
 * per-store request: the overview renders a card per datastore, and the
 * endpoint reads every job from PBS whatever the filter, so keying on `store`
 * would issue N identical round-trips for N datastores. The API's ?store= is
 * kept for external callers.
 */
export function usePruneJobs(pbsId: string, store: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "prune-jobs"],
    queryFn: () =>
      apiClient.list<PBSPruneJob>(
        apiPath`/api/v1/pbs-servers/${pbsId}/prune-jobs`,
      ),
    select: (jobs: PBSPruneJob[]) => jobs.filter((j) => j.store === store),
    enabled: pbsId.length > 0 && store.length > 0,
    staleTime: 120_000,
  });
}

// --- Backup Coverage ---

export function useBackupCoverage() {
  return useQuery({
    queryKey: ["backup-coverage"],
    queryFn: () =>
      apiClient.list<BackupCoverageEntry>(apiPath`/api/v1/backup-coverage`),
    staleTime: 60_000,
    refetchInterval: 120_000,
  });
}

// --- Veeam Backup & Replication ---
//
// The Veeam server registry is global-scope (one VBR can protect several
// Proxmox clusters), so there is no per-cluster variant of these.

export function useVeeamServers() {
  return useQuery({
    queryKey: ["veeam-servers"],
    queryFn: () => apiClient.list<VeeamServer>(apiPath`/api/v1/veeam-servers`),
  });
}

export function useCreateVeeamServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (req: CreateVeeamServerRequest) =>
      apiClient.post<VeeamServer>(apiPath`/api/v1/veeam-servers`, req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["veeam-servers"] });
    },
  });
}

export function useUpdateVeeamServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ id, ...body }: UpdateVeeamServerRequest & { id: string }) =>
      apiClient.put<VeeamServer>(apiPath`/api/v1/veeam-servers/${id}`, body),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["veeam-servers"] });
    },
  });
}

export function useDeleteVeeamServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete<never>(apiPath`/api/v1/veeam-servers/${id}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["veeam-servers"] });
    },
  });
}

/**
 * Tests a registered server's stored connection. Deliberately a mutation
 * rather than a query: it makes an outbound authenticated call and must only
 * run when the operator asks, never on mount or on a window refocus.
 */
export function useTestVeeamServer() {
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post<VeeamProbeResult>(
        apiPath`/api/v1/veeam-servers/${id}/test`,
      ),
  });
}

export function useVeeamRepositories(serverId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "repositories"],
    queryFn: () =>
      apiClient.list<VeeamRepository>(
        apiPath`/api/v1/veeam-servers/${serverId}/repositories`,
      ),
    enabled: serverId.length > 0,
  });
}

export function useVeeamRepositoryMetrics(
  serverId: string,
  repositoryVeeamId: string,
  range = "7d",
) {
  return useQuery({
    queryKey: [
      "veeam-servers",
      serverId,
      "repositories",
      repositoryVeeamId,
      "metrics",
      range,
    ],
    queryFn: () =>
      apiClient.list<VeeamRepositoryMetric>(
        apiPath`/api/v1/veeam-servers/${serverId}/repositories/${repositoryVeeamId}/metrics?range=${range}`,
      ),
    enabled: serverId.length > 0 && repositoryVeeamId.length > 0,
  });
}

export function useVeeamJobs(serverId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "jobs"],
    queryFn: () =>
      apiClient.list<VeeamJob>(apiPath`/api/v1/veeam-servers/${serverId}/jobs`),
    enabled: serverId.length > 0,
    // A running job's progress moves; the collector refreshes job state on
    // its own cadence, so this only has to keep the page roughly current.
    refetchInterval: 60_000,
  });
}

export function useVeeamSessions(serverId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "sessions"],
    queryFn: () =>
      apiClient.list<VeeamSession>(
        apiPath`/api/v1/veeam-servers/${serverId}/sessions`,
      ),
    enabled: serverId.length > 0,
    refetchInterval: 60_000,
  });
}

export function useVeeamBackupObjects(serverId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "backup-objects"],
    queryFn: () =>
      apiClient.list<VeeamBackupObject>(
        apiPath`/api/v1/veeam-servers/${serverId}/backup-objects`,
      ),
    enabled: serverId.length > 0,
  });
}

export function useVeeamRestorePoints(serverId: string, objectId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "backup-objects", objectId, "points"],
    queryFn: () =>
      apiClient.list<VeeamRestorePoint>(
        apiPath`/api/v1/veeam-servers/${serverId}/backup-objects/${objectId}/restore-points`,
      ),
    enabled: serverId.length > 0 && objectId.length > 0,
  });
}

/**
 * The Proxmox connections a Veeam server has been seen protecting, with the
 * Nexara cluster an operator has mapped each to.
 */
export function useVeeamPlatforms(serverId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "platforms"],
    queryFn: () =>
      apiClient.list<VeeamPlatform>(
        apiPath`/api/v1/veeam-servers/${serverId}/platforms`,
      ),
    enabled: serverId.length > 0,
  });
}

/**
 * Attaches a Veeam platform to a Nexara cluster, or detaches it with a null
 * cluster.
 *
 * Invalidates every listing for the server, not just the platform one: the
 * mapping is what makes jobs, sessions, backup objects and coverage
 * attributable, so all of them change meaning the moment it does.
 */
export function useMapVeeamPlatform(serverId: string) {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      platformId,
      clusterId,
    }: {
      platformId: string;
      clusterId: string | null;
    }) =>
      apiClient.put<VeeamPlatform>(
        apiPath`/api/v1/veeam-servers/${serverId}/platforms/${platformId}`,
        { cluster_id: clusterId },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["veeam-servers"] });
      void queryClient.invalidateQueries({ queryKey: ["backup-coverage"] });
      // A platform mapping changes protection for every guest on the cluster
      // at once, so every VM detail card is now wrong.
      void queryClient.invalidateQueries({
        queryKey: ["veeam-guest-protection"],
      });
    },
  });
}

/** The guests belonging to the Veeam deployment itself. */
export function useVeeamInfrastructure(serverId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "infrastructure"],
    queryFn: () =>
      apiClient.list<VeeamInfrastructureGuest>(
        apiPath`/api/v1/veeam-servers/${serverId}/infrastructure`,
      ),
    enabled: serverId.length > 0,
  });
}

/** Backup objects whose platform is mapped but which match no guest on it. */
export function useVeeamOrphanedObjects(serverId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "orphaned-objects"],
    queryFn: () =>
      apiClient.list<VeeamOrphanedObject>(
        apiPath`/api/v1/veeam-servers/${serverId}/orphaned-objects`,
      ),
    enabled: serverId.length > 0,
  });
}

/**
 * Pins a backup object to a guest, or clears the pin with nulls.
 *
 * The escape hatch for what automatic resolution cannot know. The guest must
 * be on the cluster the object's platform is mapped to — a Veeam platformId is
 * one Proxmox connection, so its objects cannot belong anywhere else.
 */
export function useMapVeeamBackupObject(serverId: string) {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      objectId,
      clusterId,
      vmid,
    }: {
      objectId: string;
      clusterId: string | null;
      vmid: number | null;
    }) =>
      apiClient.put<{ id: string; match_method: string }>(
        apiPath`/api/v1/veeam-servers/${serverId}/backup-objects/${objectId}/guest`,
        { cluster_id: clusterId, vmid },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["veeam-servers"] });
      void queryClient.invalidateQueries({ queryKey: ["backup-coverage"] });
      // The whole point of a manual map is that a guest's VM detail card
      // starts showing the backup. Without this it would go on saying there
      // is none until the five-minute staleTime expired, and read exactly
      // like a mapping that had silently failed.
      void queryClient.invalidateQueries({
        queryKey: ["veeam-guest-protection"],
      });
    },
  });
}

/**
 * Veeam job control.
 *
 * Every one of these is ASYNC on the Veeam side: a 2xx means Veeam accepted
 * the request, not that the job has started or stopped. The lab job took ~30s
 * to reach Stopped after its stop returned — so the invalidation below is what
 * begins showing the real state, and the button must not claim the action is
 * complete.
 */
interface VeeamJobStartResult {
  started: boolean;
  session_id?: string;
  state?: string;
  /**
   * Present for either outcome that is not a plain "a run started": the job
   * had no objects to process (started false), or Veeam accepted the request
   * but reported no run to track (started true, no session_id).
   */
  message?: string;
}

interface VeeamJobStopResult {
  /** Always true on a 2xx — a stop Veeam accepted is a stop in progress. */
  stopping: boolean;
  /** Absent only when Veeam reported no run and the job had no last known one. */
  session_id?: string;
  state?: string;
}

/**
 * Invalidates everything a control action can have changed.
 *
 * Broad on purpose. A start creates a session, moves the job's status, and
 * will eventually move coverage and the per-guest protection cards — and the
 * operator's next question after clicking is always "did it take", which a
 * stale table answers wrongly.
 */
function useVeeamControlInvalidation() {
  const queryClient = useQueryClient();
  return () => {
    void queryClient.invalidateQueries({ queryKey: ["veeam-servers"] });
    void queryClient.invalidateQueries({
      queryKey: ["veeam-guest-protection"],
    });
  };
}

export function useStartVeeamJob(serverId: string) {
  const invalidate = useVeeamControlInvalidation();
  return useMutation({
    mutationFn: (jobVeeamId: string) =>
      apiClient.post<VeeamJobStartResult>(
        apiPath`/api/v1/veeam-servers/${serverId}/jobs/${jobVeeamId}/start`,
      ),
    onSuccess: invalidate,
  });
}

export function useStopVeeamJob(serverId: string) {
  const invalidate = useVeeamControlInvalidation();
  return useMutation({
    mutationFn: (jobVeeamId: string) =>
      apiClient.post<VeeamJobStopResult>(
        apiPath`/api/v1/veeam-servers/${serverId}/jobs/${jobVeeamId}/stop`,
      ),
    onSuccess: invalidate,
  });
}

/**
 * Enables or disables a job's schedule.
 *
 * The stored job row keeps its old status until the next collector poll —
 * veeam_jobs mirrors what Veeam reports and the server deliberately does not
 * write a status Veeam has not confirmed — so the refetch this triggers may
 * still show the previous value for a cycle.
 */
export function useSetVeeamJobEnabled(serverId: string) {
  const invalidate = useVeeamControlInvalidation();
  return useMutation({
    mutationFn: ({
      jobVeeamId,
      enabled,
    }: {
      jobVeeamId: string;
      enabled: boolean;
    }) =>
      apiClient.post<{ enabled: boolean }>(
        apiPath`/api/v1/veeam-servers/${serverId}/jobs/${jobVeeamId}/${enabled ? "enable" : "disable"}`,
      ),
    onSuccess: invalidate,
  });
}

export function useStopVeeamSession(serverId: string) {
  const invalidate = useVeeamControlInvalidation();
  return useMutation({
    mutationFn: (sessionVeeamId: string) =>
      apiClient.post<{ stopping: boolean }>(
        apiPath`/api/v1/veeam-servers/${serverId}/sessions/${sessionVeeamId}/stop`,
      ),
    onSuccess: invalidate,
  });
}

/**
 * One session's log, read live from Veeam rather than from Nexara's tables.
 *
 * AN EMPTY RESULT IS NORMAL for a stopped run: Veeam keeps no records at all
 * for a killed session. The caller must say so, because "the log is empty" and
 * "we could not fetch the log" look identical otherwise.
 *
 * Fetched only when a row is expanded — each call costs a fresh logon against
 * the Veeam server, so this must never be eager.
 */
export function useVeeamSessionLogs(
  serverId: string,
  sessionVeeamId: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "sessions", sessionVeeamId, "logs"],
    queryFn: () =>
      apiClient.list<VeeamSessionLogRecord>(
        apiPath`/api/v1/veeam-servers/${serverId}/sessions/${sessionVeeamId}/logs`,
      ),
    enabled: enabled && serverId.length > 0 && sessionVeeamId.length > 0,
    // A finished session's log does not change, and a caller without
    // view:veeam on the cluster gets a 403 that retrying cannot fix.
    staleTime: 5 * 60_000,
    retry: false,
  });
}

/**
 * The per-guest breakdown of one run.
 *
 * AN EMPTY RESULT IS TWO DIFFERENT FACTS. Veeam reports no task rows for a run
 * still in flight — they appear as tasks finish — and none for a finished run
 * it kept no detail for. The caller knows which from the run's own state, and
 * must say so: "not reported yet" and "no detail" read identically otherwise.
 *
 * Fetched only when a row is expanded — each call costs a fresh logon against
 * the Veeam server, so this must never be eager.
 */
export function useVeeamSessionTasks(
  serverId: string,
  sessionVeeamId: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "sessions", sessionVeeamId, "tasks"],
    queryFn: () =>
      apiClient.list<VeeamTaskSession>(
        apiPath`/api/v1/veeam-servers/${serverId}/sessions/${sessionVeeamId}/tasks`,
      ),
    enabled: enabled && serverId.length > 0 && sessionVeeamId.length > 0,
    // A finished run's breakdown does not change, and a caller without
    // view:veeam on the cluster gets a 403 that retrying cannot fix.
    staleTime: 5 * 60_000,
    retry: false,
  });
}

/**
 * One guest's Veeam protection, for the VM detail page.
 *
 * Takes the guest's UUID like every sibling route; the server resolves it to
 * the stable (cluster_id, vmid) identity for the lookup.
 */
export function useVeeamGuestProtection(clusterId: string, vmId: string) {
  return useQuery({
    // Its own prefix rather than nesting under ["clusters"], so the two
    // mapping mutations can invalidate exactly this and nothing else. Nested
    // under the cluster key they would either miss it — leaving the card
    // claiming "no Veeam data" for the five-minute staleTime after a
    // successful map — or force a refetch of every cluster-scoped query.
    queryKey: ["veeam-guest-protection", clusterId, vmId],
    queryFn: () =>
      apiClient.get<VeeamGuestProtection>(
        apiPath`/api/v1/clusters/${clusterId}/vms/${vmId}/veeam`,
      ),
    enabled: clusterId.length > 0 && vmId.length > 0,
    // A viewer without view:veeam on this cluster gets a 403, and a guest
    // Veeam has never seen is a perfectly ordinary answer — neither is worth
    // retrying.
    retry: false,
  });
}
