import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  RollingUpdateJob,
  RollingUpdateNode,
  AptPackage,
  CreateRollingUpdateRequest,
  HAPreFlightReport,
  SSHCredential,
  SSHTestResponse,
} from "@/types/api";

export function useRollingUpdateJobs(clusterId: string) {
  return useQuery({
    queryKey: ["rolling-update-jobs", clusterId],
    queryFn: () =>
      apiClient.get<RollingUpdateJob[]>(
        `/api/v1/clusters/${clusterId}/rolling-updates?limit=50`,
      ),
    enabled: !!clusterId,
  });
}

export function useRollingUpdateJob(clusterId: string, jobId: string) {
  return useQuery({
    queryKey: ["rolling-update-job", clusterId, jobId],
    queryFn: () =>
      apiClient.get<RollingUpdateJob>(
        `/api/v1/clusters/${clusterId}/rolling-updates/${jobId}`,
      ),
    enabled: !!clusterId && !!jobId,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data) return false;
      return data.status === "running" || data.status === "paused"
        ? 5000
        : false;
    },
  });
}

export function useRollingUpdateNodes(clusterId: string, jobId: string) {
  return useQuery({
    queryKey: ["rolling-update-nodes", clusterId, jobId],
    queryFn: () =>
      apiClient.get<RollingUpdateNode[]>(
        `/api/v1/clusters/${clusterId}/rolling-updates/${jobId}/nodes`,
      ),
    enabled: !!clusterId && !!jobId,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data) return false;
      const hasActive = data.some(
        (n) =>
          n.step === "draining" ||
          n.step === "awaiting_upgrade" ||
          n.step === "upgrading" ||
          n.step === "rebooting" ||
          n.step === "health_check" ||
          n.step === "restoring",
      );
      return hasActive ? 5000 : false;
    },
  });
}

export function useCreateRollingUpdateJob() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      ...body
    }: CreateRollingUpdateRequest & { clusterId: string }) =>
      apiClient.post<RollingUpdateJob>(
        `/api/v1/clusters/${clusterId}/rolling-updates`,
        body,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["rolling-update-jobs", vars.clusterId],
      });
    },
  });
}

export function useStartRollingUpdateJob() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      jobId,
    }: {
      clusterId: string;
      jobId: string;
    }) =>
      apiClient.post<RollingUpdateJob>(
        `/api/v1/clusters/${clusterId}/rolling-updates/${jobId}/start`,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["rolling-update-jobs", vars.clusterId],
      });
      void qc.invalidateQueries({
        queryKey: ["rolling-update-job", vars.clusterId, vars.jobId],
      });
    },
  });
}

export function useCancelRollingUpdateJob() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      jobId,
    }: {
      clusterId: string;
      jobId: string;
    }) =>
      apiClient.post(
        `/api/v1/clusters/${clusterId}/rolling-updates/${jobId}/cancel`,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["rolling-update-jobs", vars.clusterId],
      });
      void qc.invalidateQueries({
        queryKey: ["rolling-update-job", vars.clusterId, vars.jobId],
      });
    },
  });
}

export function usePauseRollingUpdateJob() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      jobId,
    }: {
      clusterId: string;
      jobId: string;
    }) =>
      apiClient.post(
        `/api/v1/clusters/${clusterId}/rolling-updates/${jobId}/pause`,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["rolling-update-jobs", vars.clusterId],
      });
      void qc.invalidateQueries({
        queryKey: ["rolling-update-job", vars.clusterId, vars.jobId],
      });
    },
  });
}

export function useResumeRollingUpdateJob() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      jobId,
    }: {
      clusterId: string;
      jobId: string;
    }) =>
      apiClient.post(
        `/api/v1/clusters/${clusterId}/rolling-updates/${jobId}/resume`,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["rolling-update-jobs", vars.clusterId],
      });
      void qc.invalidateQueries({
        queryKey: ["rolling-update-job", vars.clusterId, vars.jobId],
      });
    },
  });
}

export function useConfirmNodeUpgrade() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      jobId,
      nodeId,
    }: {
      clusterId: string;
      jobId: string;
      nodeId: string;
    }) =>
      apiClient.post(
        `/api/v1/clusters/${clusterId}/rolling-updates/${jobId}/nodes/${nodeId}/confirm-upgrade`,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["rolling-update-nodes", vars.clusterId, vars.jobId],
      });
      void qc.invalidateQueries({
        queryKey: ["rolling-update-job", vars.clusterId, vars.jobId],
      });
    },
  });
}

export function useSkipNode() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      jobId,
      nodeId,
    }: {
      clusterId: string;
      jobId: string;
      nodeId: string;
    }) =>
      apiClient.post(
        `/api/v1/clusters/${clusterId}/rolling-updates/${jobId}/nodes/${nodeId}/skip`,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["rolling-update-nodes", vars.clusterId, vars.jobId],
      });
      void qc.invalidateQueries({
        queryKey: ["rolling-update-job", vars.clusterId, vars.jobId],
      });
    },
  });
}

export function useNodePackagePreview(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["node-packages", clusterId, nodeName],
    queryFn: () =>
      apiClient.get<AptPackage[]>(
        `/api/v1/clusters/${clusterId}/nodes/${nodeName}/packages`,
      ),
    enabled: !!clusterId && !!nodeName,
  });
}

export function usePreflightHA() {
  return useMutation({
    mutationFn: ({
      clusterId,
      nodes,
    }: {
      clusterId: string;
      nodes: string[];
    }) =>
      apiClient.post<HAPreFlightReport>(
        `/api/v1/clusters/${clusterId}/rolling-updates/preflight-ha`,
        { nodes },
      ),
  });
}

// --- SSH Credential Hooks ---

export function useSSHCredentials(clusterId: string) {
  return useQuery({
    queryKey: ["ssh-credentials", clusterId],
    queryFn: () =>
      apiClient.get<SSHCredential | null>(
        `/api/v1/clusters/${clusterId}/ssh-credentials`,
      ),
    enabled: !!clusterId,
  });
}

export function useUpsertSSHCredentials() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      ...body
    }: {
      clusterId: string;
      username: string;
      port: number;
      auth_type: "password" | "key";
      password?: string;
      private_key?: string;
    }) =>
      apiClient.put<SSHCredential>(
        `/api/v1/clusters/${clusterId}/ssh-credentials`,
        body,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["ssh-credentials", vars.clusterId],
      });
    },
  });
}

export function useDeleteSSHCredentials() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId }: { clusterId: string }) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/ssh-credentials`),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["ssh-credentials", vars.clusterId],
      });
    },
  });
}

export function useTestSSHConnection() {
  return useMutation({
    mutationFn: ({
      clusterId,
      nodeName,
    }: {
      clusterId: string;
      nodeName: string;
    }) =>
      apiClient.post<SSHTestResponse>(
        `/api/v1/clusters/${clusterId}/ssh-credentials/test`,
        { node_name: nodeName },
      ),
  });
}
