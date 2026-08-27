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
} from "../types/backup";

// --- PBS Server Queries ---

export function usePBSServers() {
  return useQuery({
    queryKey: ["pbs-servers"],
    queryFn: () => apiClient.list<PBSServer>("/api/v1/pbs-servers"),
  });
}

// --- Datastore Queries (live proxy) ---

export function usePBSDatastores(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "datastores"],
    queryFn: () =>
      apiClient.list<PBSDatastore>(
        `/api/v1/pbs-servers/${pbsId}/datastores`,
      ),
    enabled: pbsId.length > 0,
  });
}

export function usePBSDatastoreStatus(pbsId: string) {
  return useQuery({
    queryKey: ["pbs-servers", pbsId, "datastores", "status"],
    queryFn: () =>
      apiClient.list<PBSDatastoreStatus>(
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
      apiClient.list<PBSSnapshot>(
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
      apiClient.list<PBSSnapshot>(
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
      apiClient.list<PBSSyncJob>(
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
      apiClient.list<PBSVerifyJob>(
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
      apiClient.list<PBSTask>(
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
      apiClient.list<PBSDatastoreMetric>(
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
      apiClient.list<PBSDatastoreRRDEntry>(
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
  allow_private_address?: boolean;
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
  allow_private_address?: boolean;
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
      apiClient.list<PBSTaskLogEntry>(
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
      apiClient.list<BackupJob>(
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
      apiClient.list<PBSPruneJob>(`/api/v1/pbs-servers/${pbsId}/prune-jobs`),
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
      apiClient.list<BackupCoverageEntry>("/api/v1/backup-coverage"),
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
    queryFn: () => apiClient.list<VeeamServer>("/api/v1/veeam-servers"),
  });
}

export function useCreateVeeamServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (req: CreateVeeamServerRequest) =>
      apiClient.post<VeeamServer>("/api/v1/veeam-servers", req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["veeam-servers"] });
    },
  });
}

export function useUpdateVeeamServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ id, ...body }: UpdateVeeamServerRequest & { id: string }) =>
      apiClient.put<VeeamServer>(`/api/v1/veeam-servers/${id}`, body),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["veeam-servers"] });
    },
  });
}

export function useDeleteVeeamServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete<never>(`/api/v1/veeam-servers/${id}`),
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
      apiClient.post<VeeamProbeResult>(`/api/v1/veeam-servers/${id}/test`),
  });
}

export function useVeeamRepositories(serverId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "repositories"],
    queryFn: () =>
      apiClient.list<VeeamRepository>(
        `/api/v1/veeam-servers/${serverId}/repositories`,
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
        `/api/v1/veeam-servers/${serverId}/repositories/${repositoryVeeamId}/metrics?range=${range}`,
      ),
    enabled: serverId.length > 0 && repositoryVeeamId.length > 0,
  });
}

export function useVeeamJobs(serverId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "jobs"],
    queryFn: () =>
      apiClient.list<VeeamJob>(`/api/v1/veeam-servers/${serverId}/jobs`),
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
        `/api/v1/veeam-servers/${serverId}/sessions`,
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
        `/api/v1/veeam-servers/${serverId}/backup-objects`,
      ),
    enabled: serverId.length > 0,
  });
}

export function useVeeamRestorePoints(serverId: string, objectId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "backup-objects", objectId, "points"],
    queryFn: () =>
      apiClient.list<VeeamRestorePoint>(
        `/api/v1/veeam-servers/${serverId}/backup-objects/${objectId}/restore-points`,
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
        `/api/v1/veeam-servers/${serverId}/platforms`,
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
        `/api/v1/veeam-servers/${serverId}/platforms/${platformId}`,
        { cluster_id: clusterId },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["veeam-servers"] });
      void queryClient.invalidateQueries({ queryKey: ["backup-coverage"] });
      // A platform mapping changes protection for every guest on the cluster
      // at once, so every VM detail card is now wrong.
      void queryClient.invalidateQueries({ queryKey: ["veeam-guest-protection"] });
    },
  });
}

/** The guests belonging to the Veeam deployment itself. */
export function useVeeamInfrastructure(serverId: string) {
  return useQuery({
    queryKey: ["veeam-servers", serverId, "infrastructure"],
    queryFn: () =>
      apiClient.list<VeeamInfrastructureGuest>(
        `/api/v1/veeam-servers/${serverId}/infrastructure`,
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
        `/api/v1/veeam-servers/${serverId}/orphaned-objects`,
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
        `/api/v1/veeam-servers/${serverId}/backup-objects/${objectId}/guest`,
        { cluster_id: clusterId, vmid },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["veeam-servers"] });
      void queryClient.invalidateQueries({ queryKey: ["backup-coverage"] });
      // The whole point of a manual map is that a guest's VM detail card
      // starts showing the backup. Without this it would go on saying there
      // is none until the five-minute staleTime expired, and read exactly
      // like a mapping that had silently failed.
      void queryClient.invalidateQueries({ queryKey: ["veeam-guest-protection"] });
    },
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
        `/api/v1/clusters/${clusterId}/vms/${vmId}/veeam`,
      ),
    enabled: clusterId.length > 0 && vmId.length > 0,
    // A viewer without view:veeam on this cluster gets a 403, and a guest
    // Veeam has never seen is a perfectly ordinary answer — neither is worth
    // retrying.
    retry: false,
  });
}
