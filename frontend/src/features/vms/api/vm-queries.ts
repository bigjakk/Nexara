import { useMutation, useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { VMResponse } from "@/types/api";
import type {
  VMAction,
  VMActionResponse,
  CloneRequest,
  MigrateRequest,
  TaskStatusResponse,
  ResourceKind,
  Snapshot,
  SnapshotRequest,
  CreateVMRequest,
  CreateCTRequest,
  VMConfig,
} from "../types/vm";

// --- All VMIDs in a cluster (for next-available-ID calculation) ---

export function useClusterVMIDs(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "vmids"],
    queryFn: async () => {
      const vms = await apiClient.get<VMResponse[]>(
        `/api/v1/clusters/${clusterId}/vms`,
      );
      return new Set(vms.map((vm) => vm.vmid));
    },
    enabled: clusterId.length > 0,
    staleTime: 10_000, // refetch after 10s so deleted VMs are cleared quickly
  });
}

// --- Resource pools in a cluster ---

export interface ResourcePool {
  poolid: string;
  comment?: string;
}

export function useResourcePools(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "pools"],
    queryFn: () =>
      apiClient.get<ResourcePool[]>(
        `/api/v1/clusters/${clusterId}/pools`,
      ),
    enabled: clusterId.length > 0,
    staleTime: 60_000,
  });
}

// --- Single VM/CT fetch ---

export function useVM(clusterId: string, vmId: string, kind: ResourceKind) {
  const endpoint =
    kind === "ct"
      ? `/api/v1/clusters/${clusterId}/containers/${vmId}`
      : `/api/v1/clusters/${clusterId}/vms/${vmId}`;

  return useQuery({
    queryKey: ["clusters", clusterId, kind === "ct" ? "containers" : "vms", vmId],
    queryFn: () => apiClient.get<VMResponse>(endpoint),
    enabled: clusterId.length > 0 && vmId.length > 0,
    refetchInterval: 10_000,
  });
}

// --- Lifecycle action (start/stop/shutdown/reboot/reset/suspend/resume) ---

interface ActionParams {
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  action: VMAction;
}

export function useVMAction() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, resourceId, kind, action }: ActionParams) => {
      const base =
        kind === "ct"
          ? `/api/v1/clusters/${clusterId}/containers/${resourceId}/status`
          : `/api/v1/clusters/${clusterId}/vms/${resourceId}/status`;
      return apiClient.post<VMActionResponse>(base, { action });
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "containers"],
      });
    },
  });
}

// --- Clone ---

interface CloneParams {
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  body: CloneRequest;
}

export function useCloneVM() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, resourceId, kind, body }: CloneParams) => {
      const base =
        kind === "ct"
          ? `/api/v1/clusters/${clusterId}/containers/${resourceId}/clone`
          : `/api/v1/clusters/${clusterId}/vms/${resourceId}/clone`;
      return apiClient.post<VMActionResponse>(base, body);
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vmids"],
      });
    },
  });
}

// --- Migrate (CT only) ---

interface MigrateParams {
  clusterId: string;
  containerId: string;
  body: MigrateRequest;
}

export function useMigrateContainer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, containerId, body }: MigrateParams) =>
      apiClient.post<VMActionResponse>(
        `/api/v1/clusters/${clusterId}/containers/${containerId}/migrate`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "containers"],
      });
    },
  });
}

// --- Destroy ---

interface DestroyParams {
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
}

export function useDestroyVM() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, resourceId, kind }: DestroyParams) => {
      const base =
        kind === "ct"
          ? `/api/v1/clusters/${clusterId}/containers/${resourceId}`
          : `/api/v1/clusters/${clusterId}/vms/${resourceId}`;
      return apiClient.delete<VMActionResponse>(base);
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "containers"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vmids"],
      });
    },
  });
}

// --- Task status polling ---

export function useTaskStatus(clusterId: string, upid: string | null) {
  return useQuery({
    queryKey: ["clusters", clusterId, "tasks", upid],
    queryFn: () =>
      apiClient.get<TaskStatusResponse>(
        `/api/v1/clusters/${clusterId}/tasks/${encodeURIComponent(upid ?? "")}`,
      ),
    enabled: upid !== null && upid.length > 0 && clusterId.length > 0,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (data && data.status === "stopped") return false;
      return 2000;
    },
  });
}

// --- Snapshots ---

export function useSnapshots(
  clusterId: string,
  resourceId: string,
  kind: ResourceKind,
) {
  const base =
    kind === "ct"
      ? `/api/v1/clusters/${clusterId}/containers/${resourceId}/snapshots`
      : `/api/v1/clusters/${clusterId}/vms/${resourceId}/snapshots`;

  return useQuery({
    queryKey: [
      "clusters",
      clusterId,
      kind === "ct" ? "containers" : "vms",
      resourceId,
      "snapshots",
    ],
    queryFn: () => apiClient.get<Snapshot[]>(base),
    enabled: clusterId.length > 0 && resourceId.length > 0,
  });
}

interface CreateSnapshotParams {
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  body: SnapshotRequest;
}

export function useCreateSnapshot() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, resourceId, kind, body }: CreateSnapshotParams) => {
      const base =
        kind === "ct"
          ? `/api/v1/clusters/${clusterId}/containers/${resourceId}/snapshots`
          : `/api/v1/clusters/${clusterId}/vms/${resourceId}/snapshots`;
      return apiClient.post<VMActionResponse>(base, body);
    },
    onSuccess: (_data, variables) => {
      const coll = variables.kind === "ct" ? "containers" : "vms";
      void queryClient.invalidateQueries({
        queryKey: [
          "clusters",
          variables.clusterId,
          coll,
          variables.resourceId,
          "snapshots",
        ],
      });
    },
  });
}

interface DeleteSnapshotParams {
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  snapName: string;
}

export function useDeleteSnapshot() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      clusterId,
      resourceId,
      kind,
      snapName,
    }: DeleteSnapshotParams) => {
      const base =
        kind === "ct"
          ? `/api/v1/clusters/${clusterId}/containers/${resourceId}/snapshots/${encodeURIComponent(snapName)}`
          : `/api/v1/clusters/${clusterId}/vms/${resourceId}/snapshots/${encodeURIComponent(snapName)}`;
      return apiClient.delete<VMActionResponse>(base);
    },
    onSuccess: (_data, variables) => {
      const coll = variables.kind === "ct" ? "containers" : "vms";
      void queryClient.invalidateQueries({
        queryKey: [
          "clusters",
          variables.clusterId,
          coll,
          variables.resourceId,
          "snapshots",
        ],
      });
    },
  });
}

interface RollbackSnapshotParams {
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  snapName: string;
}

export function useRollbackSnapshot() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      clusterId,
      resourceId,
      kind,
      snapName,
    }: RollbackSnapshotParams) => {
      const base =
        kind === "ct"
          ? `/api/v1/clusters/${clusterId}/containers/${resourceId}/snapshots/${encodeURIComponent(snapName)}/rollback`
          : `/api/v1/clusters/${clusterId}/vms/${resourceId}/snapshots/${encodeURIComponent(snapName)}/rollback`;
      return apiClient.post<VMActionResponse>(base, {});
    },
    onSuccess: (_data, variables) => {
      const coll = variables.kind === "ct" ? "containers" : "vms";
      void queryClient.invalidateQueries({
        queryKey: [
          "clusters",
          variables.clusterId,
          coll,
          variables.resourceId,
          "snapshots",
        ],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "containers"],
      });
    },
  });
}

// --- Disk Resize ---

interface ResizeDiskParams {
  clusterId: string;
  vmId: string;
  disk: string;
  size: string;
}

export function useResizeDisk() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, vmId, disk, size }: ResizeDiskParams) =>
      apiClient.post<VMActionResponse>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/disks/resize`,
        { disk, size },
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: [
          "clusters",
          variables.clusterId,
          "vms",
          variables.vmId,
          "config",
        ],
      });
    },
  });
}

// --- Create VM ---

interface CreateVMParams {
  clusterId: string;
  body: CreateVMRequest;
}

export function useCreateVM() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, body }: CreateVMParams) =>
      apiClient.post<VMActionResponse>(
        `/api/v1/clusters/${clusterId}/vms`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vmids"],
      });
    },
  });
}

// --- Create Container ---

interface CreateContainerParams {
  clusterId: string;
  body: CreateCTRequest;
}

export function useCreateContainer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, body }: CreateContainerParams) =>
      apiClient.post<VMActionResponse>(
        `/api/v1/clusters/${clusterId}/containers`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "containers"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vmids"],
      });
    },
  });
}

// --- VM Config (Cloud-Init) ---

export function useVMConfig(clusterId: string, vmId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "vms", vmId, "config"],
    queryFn: () =>
      apiClient.get<VMConfig>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/config`,
      ),
    enabled: clusterId.length > 0 && vmId.length > 0,
  });
}

interface SetVMConfigParams {
  clusterId: string;
  vmId: string;
  fields: Record<string, string>;
}

export function useSetVMConfig() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, vmId, fields }: SetVMConfigParams) =>
      apiClient.put<{ status: string }>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/config`,
        { fields },
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: [
          "clusters",
          variables.clusterId,
          "vms",
          variables.vmId,
          "config",
        ],
      });
    },
  });
}

interface SetResourceConfigParams {
  clusterId: string;
  resourceId: string;
  kind: "vm" | "ct";
  fields: Record<string, string>;
}

export function useSetResourceConfig() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, resourceId, kind, fields }: SetResourceConfigParams) => {
      const path = kind === "ct"
        ? `/api/v1/clusters/${clusterId}/containers/${resourceId}/config`
        : `/api/v1/clusters/${clusterId}/vms/${resourceId}/config`;
      return apiClient.put<{ status: string }>(path, { fields });
    },
    onSuccess: (_data, variables) => {
      const qk = ["clusters", variables.clusterId, variables.kind === "ct" ? "containers" : "vms", variables.resourceId];
      // Optimistically patch the cached VM/CT with new name if present
      const nameField = variables.kind === "ct" ? variables.fields["hostname"] : variables.fields["name"];
      if (nameField) {
        queryClient.setQueryData<VMResponse>(qk, (old) =>
          old ? { ...old, name: nameField } : old,
        );
      }
      void queryClient.invalidateQueries({ queryKey: qk });
    },
  });
}

// --- Task History ---

export interface TaskHistoryEntry {
  id: string;
  cluster_id: string;
  upid: string;
  description: string;
  status: string;
  exit_status: string;
  node: string;
  task_type: string;
  progress: number | null;
  started_at: string;
  finished_at: string | null;
}

export function useTaskHistory(): UseQueryResult<TaskHistoryEntry[]> {
  return useQuery({
    queryKey: ["task-history"],
    queryFn: () =>
      apiClient.get<TaskHistoryEntry[]>("/api/v1/tasks"),
    refetchInterval: (query) => {
      const data = query.state.data;
      if (data && data.some((t) => t.status === "running")) return 3000;
      return false;
    },
  });
}

interface AddTaskHistoryParams {
  clusterId: string;
  upid: string;
  description: string;
  node?: string;
  taskType?: string;
}

export function useAddTaskHistory() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, upid, description, node, taskType }: AddTaskHistoryParams) =>
      apiClient.post<TaskHistoryEntry>("/api/v1/tasks", {
        cluster_id: clusterId,
        upid,
        description,
        status: "running",
        node: node ?? "",
        task_type: taskType ?? "",
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["task-history"] });
    },
  });
}

interface UpdateTaskHistoryParams {
  upid: string;
  status: string;
  exitStatus?: string | undefined;
  progress?: number | null | undefined;
  finishedAt?: string | undefined;
}

export function useUpdateTaskHistory() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ upid, status, exitStatus, progress, finishedAt }: UpdateTaskHistoryParams) =>
      apiClient.put<{ status: string }>(
        `/api/v1/tasks/${encodeURIComponent(upid)}`,
        {
          status,
          exit_status: exitStatus ?? "",
          progress: progress ?? null,
          finished_at: finishedAt ?? null,
        },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["task-history"] });
    },
  });
}

export function useClearTaskHistory() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: () => apiClient.delete<{ status: string }>("/api/v1/tasks"),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["task-history"] });
    },
  });
}

// --- Guest Agent ---

export interface GuestIPAddress {
  "ip-address": string;
  "ip-address-type": string;
  prefix: number;
}

export interface GuestNetworkInterface {
  name: string;
  "hardware-address": string;
  "ip-addresses": GuestIPAddress[];
}

export interface GuestOSInfo {
  name: string;
  "kernel-version": string;
  "kernel-release": string;
  machine: string;
  id: string;
  "pretty-name": string;
  version: string;
  "version-id": string;
}

export interface GuestAgentResponse {
  running: boolean;
  os_info?: GuestOSInfo;
  network_interfaces?: GuestNetworkInterface[];
}

export function useGuestAgentInfo(
  clusterId: string,
  vmId: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["clusters", clusterId, "vms", vmId, "agent"],
    queryFn: () =>
      apiClient.get<GuestAgentResponse>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/agent`,
      ),
    enabled: enabled && clusterId.length > 0 && vmId.length > 0,
    refetchInterval: 30_000,
  });
}

// --- Disk Move ---

interface MoveDiskParams {
  clusterId: string;
  vmId: string;
  disk: string;
  storage: string;
  deleteOriginal: boolean;
}

export function useMoveDisk() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, vmId, disk, storage, deleteOriginal }: MoveDiskParams) =>
      apiClient.post<VMActionResponse>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/disks/move`,
        { disk, storage, delete: deleteOriginal },
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms", variables.vmId, "config"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "storage"],
      });
    },
  });
}

// --- Disk Attach/Detach ---

interface AttachDiskParams {
  clusterId: string;
  vmId: string;
  bus: string;
  index: number;
  storage: string;
  size: string;
  format?: string;
}

export function useAttachDisk() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, vmId, bus, index, storage, size, format }: AttachDiskParams) =>
      apiClient.post<VMActionResponse>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/disks/attach`,
        { bus, index, storage, size, format },
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms", variables.vmId, "config"],
      });
    },
  });
}

interface DetachDiskParams {
  clusterId: string;
  vmId: string;
  disk: string;
}

export function useDetachDisk() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, vmId, disk }: DetachDiskParams) =>
      apiClient.post<VMActionResponse>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/disks/detach`,
        { disk },
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "vms", variables.vmId, "config"],
      });
    },
  });
}

// --- Scheduled Tasks ---

export interface ScheduledTask {
  id: string;
  cluster_id: string;
  resource_type: string;
  resource_id: string;
  node: string;
  action: string;
  schedule: string;
  params: Record<string, unknown>;
  enabled: boolean;
  last_run_at: string | null;
  next_run_at: string | null;
  last_status: string | null;
  last_error: string | null;
  created_at: string;
  updated_at: string;
}

export function useScheduledTasks(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "schedules"],
    queryFn: () =>
      apiClient.get<ScheduledTask[]>(
        `/api/v1/clusters/${clusterId}/schedules`,
      ),
    enabled: clusterId.length > 0,
  });
}

interface CreateScheduleParams {
  clusterId: string;
  body: {
    resource_type: string;
    resource_id: string;
    node: string;
    action: string;
    schedule: string;
    params: Record<string, unknown>;
    enabled: boolean;
  };
}

export function useCreateSchedule() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, body }: CreateScheduleParams) =>
      apiClient.post<ScheduledTask>(
        `/api/v1/clusters/${clusterId}/schedules`,
        body,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "schedules"],
      });
    },
  });
}

interface DeleteScheduleParams {
  clusterId: string;
  scheduleId: string;
}

export function useDeleteSchedule() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clusterId, scheduleId }: DeleteScheduleParams) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/clusters/${clusterId}/schedules/${scheduleId}`,
      ),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", variables.clusterId, "schedules"],
      });
    },
  });
}

export interface TaskLogLine {
  n: number;
  t: string;
}

export function useTaskLog(
  clusterId: string | null,
  upid: string | null,
  enabled: boolean,
): UseQueryResult<TaskLogLine[]> {
  return useQuery({
    queryKey: ["task-log", clusterId, upid],
    queryFn: () =>
      apiClient.get<TaskLogLine[]>(
        `/api/v1/clusters/${clusterId}/tasks/${encodeURIComponent(upid!)}/log`,
      ),
    enabled: enabled && !!clusterId && !!upid,
    staleTime: 60_000,
  });
}
