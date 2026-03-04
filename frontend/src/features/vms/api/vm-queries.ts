import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { VMResponse } from "@/types/api";
import type {
  VMAction,
  VMActionResponse,
  CloneRequest,
  MigrateRequest,
  TaskStatusResponse,
  ResourceKind,
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
