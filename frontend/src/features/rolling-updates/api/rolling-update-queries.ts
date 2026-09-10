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
  SSHKnownHost,
} from "@/types/api";

export function useRollingUpdateJobs(clusterId: string) {
  return useQuery({
    queryKey: ["rolling-update-jobs", clusterId],
    queryFn: () =>
      apiClient.list<RollingUpdateJob>(
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
      apiClient.list<RollingUpdateNode>(
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

/**
 * Create a one-node in-place update job and start it, as a single operation.
 *
 * Both calls live in one mutationFn on purpose. Chaining them through the
 * create's `onSuccess` looked equivalent and was not: TanStack only invokes
 * per-call callbacks while the observer still has listeners, so unmounting the
 * component drops them silently. The panel lives inside a Radix tab, which
 * unmounts on tab switch — so clicking Update and then switching tabs while the
 * POST was in flight left the job created but never started.
 *
 * That failure is worse than it sounds. HasRunningJobForCluster counts
 * 'pending' as active, so one orphan job makes every later rolling update on
 * that cluster — this panel and the cluster-wide wizard both — refuse with
 * "A rolling update job is already active", until someone finds and cancels a
 * job they never knew existed. Awaiting both calls inside the mutation means
 * neither can be stranded by a render.
 */
export function useStartInPlaceNodeUpdate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      clusterId,
      nodeName,
    }: {
      clusterId: string;
      nodeName: string;
    }) => {
      const job = await apiClient.post<RollingUpdateJob>(
        `/api/v1/clusters/${clusterId}/rolling-updates`,
        {
          nodes: [nodeName],
          parallelism: 1,
          // In place: no migration, no target node needed, guests keep running.
          drain_guests: false,
          // Nothing was moved, so there is nothing to move back. Explicit
          // rather than defaulted so the intent survives a default change.
          auto_restore_guests: false,
          // Not requested, and for an in-place job that is binding: the
          // orchestrator defers any reboot apt asks for and flags the node.
          reboot_after_update: false,
          auto_upgrade: true,
          package_excludes: [],
          ha_policy: "warn",
        },
      );
      await apiClient.post<RollingUpdateJob>(
        `/api/v1/clusters/${clusterId}/rolling-updates/${job.id}/start`,
      );
      return job;
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({
        queryKey: ["rolling-update-jobs", vars.clusterId],
      });
      // The node's pending-package list is now stale — the upgrade is about to
      // remove the very rows the panel is still showing next to an armed
      // button. Without this it keeps them for the 5 minute staleTime, and a
      // second click creates a job that immediately skips the node.
      void qc.invalidateQueries({
        queryKey: ["node-packages", vars.clusterId, vars.nodeName],
      });
    },
  });
}

export function useStartRollingUpdateJob() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, jobId }: { clusterId: string; jobId: string }) =>
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
    mutationFn: ({ clusterId, jobId }: { clusterId: string; jobId: string }) =>
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
    mutationFn: ({ clusterId, jobId }: { clusterId: string; jobId: string }) =>
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
    mutationFn: ({ clusterId, jobId }: { clusterId: string; jobId: string }) =>
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
      apiClient.list<AptPackage>(
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
      parallelism,
    }: {
      clusterId: string;
      nodes: string[];
      parallelism?: number;
    }) =>
      apiClient.post<HAPreFlightReport>(
        `/api/v1/clusters/${clusterId}/rolling-updates/preflight-ha`,
        { nodes, parallelism: parallelism ?? 1 },
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
    onSuccess: (data, vars) => {
      // Seed the cache so the view-mode form (which hosts the bulk-pin
      // dialog) renders on the next tick instead of flashing the
      // "no credentials" block while the refetch is in flight.
      qc.setQueryData(["ssh-credentials", vars.clusterId], data);
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

export function useSSHKnownHosts(clusterId: string) {
  return useQuery({
    queryKey: ["ssh-known-hosts", clusterId],
    queryFn: () =>
      apiClient.list<SSHKnownHost>(
        `/api/v1/clusters/${clusterId}/ssh-known-hosts`,
      ),
    enabled: !!clusterId,
  });
}

export function usePinSSHHostKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      nodeName,
      expectedFingerprint,
    }: {
      clusterId: string;
      nodeName: string;
      expectedFingerprint: string;
    }) =>
      apiClient.post<SSHKnownHost>(
        `/api/v1/clusters/${clusterId}/ssh-known-hosts`,
        { node_name: nodeName, expected_fingerprint: expectedFingerprint },
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["ssh-known-hosts", vars.clusterId],
      });
    },
  });
}

export function useDeleteSSHKnownHost() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, id }: { clusterId: string; id: string }) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/ssh-known-hosts/${id}`),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["ssh-known-hosts", vars.clusterId],
      });
    },
  });
}
