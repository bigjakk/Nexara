import {
  useQuery,
  useQueries,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  ClusterResponse,
  CreateClusterRequest,
  CreateClusterResponse,
  UpdateClusterResponse,
  NodeResponse,
  VMResponse,
  StorageResponse,
} from "@/types/api";

export function useClusters() {
  return useQuery({
    queryKey: ["clusters"],
    queryFn: () => apiClient.get<ClusterResponse[]>("/api/v1/clusters"),
    // Refresh periodically so cluster status and Ceph health surfaced in the
    // header/dashboard/sidebar stay current without a manual reload.
    refetchInterval: 30_000,
  });
}

export interface ClusterSummary {
  cluster: ClusterResponse;
  nodeCount: number;
  nodesOnline: number;
  vmCount: number;
  vmsRunning: number;
  containerCount: number;
  containersRunning: number;
  storageTotalBytes: number;
  storageUsedBytes: number;
}

export interface DashboardData {
  clusters: ClusterSummary[];
  totalNodes: number;
  totalNodesOnline: number;
  totalVMs: number;
  totalVMsRunning: number;
  totalContainers: number;
  totalContainersRunning: number;
  totalStorageBytes: number;
  totalStorageUsedBytes: number;
  /** VM UUID → display name (e.g. "web-01") for resolving metric IDs. */
  vmNameMap: Map<string, string>;
}

export function useDashboardData() {
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];

  const nodeQueries = useQueries({
    queries: clusters.map((cluster) => ({
      queryKey: ["clusters", cluster.id, "nodes"],
      queryFn: () =>
        apiClient.get<NodeResponse[]>(`/api/v1/clusters/${cluster.id}/nodes`),
      enabled: clusters.length > 0,
    })),
  });

  const vmQueries = useQueries({
    queries: clusters.map((cluster) => ({
      queryKey: ["clusters", cluster.id, "vms"],
      queryFn: () =>
        apiClient.get<VMResponse[]>(`/api/v1/clusters/${cluster.id}/vms`),
      enabled: clusters.length > 0,
    })),
  });

  const storageQueries = useQueries({
    queries: clusters.map((cluster) => ({
      queryKey: ["clusters", cluster.id, "storage"],
      queryFn: () =>
        apiClient.get<StorageResponse[]>(
          `/api/v1/clusters/${cluster.id}/storage`,
        ),
      enabled: clusters.length > 0,
    })),
  });

  const isLoading =
    clustersQuery.isLoading ||
    nodeQueries.some((q) => q.isLoading) ||
    vmQueries.some((q) => q.isLoading) ||
    storageQueries.some((q) => q.isLoading);

  const error =
    clustersQuery.error ??
    nodeQueries.find((q) => q.error)?.error ??
    vmQueries.find((q) => q.error)?.error ??
    storageQueries.find((q) => q.error)?.error ??
    null;

  let data: DashboardData | undefined;

  if (!isLoading && !error && clustersQuery.data) {
    let totalNodes = 0;
    let totalNodesOnline = 0;
    let totalVMs = 0;
    let totalVMsRunning = 0;
    let totalContainers = 0;
    let totalContainersRunning = 0;
    let totalStorageBytes = 0;
    let totalStorageUsedBytes = 0;
    const vmNameMap = new Map<string, string>();

    const clusterSummaries = clusters.map((cluster, i) => {
      const nodes = nodeQueries[i]?.data ?? [];
      const vms = vmQueries[i]?.data ?? [];
      const storage = storageQueries[i]?.data ?? [];

      const nodesOnline = nodes.filter((n) => n.status === "online").length;
      const vmCount = vms.filter((v) => v.type === "qemu").length;
      const vmsRunning = vms.filter(
        (v) => v.type === "qemu" && v.status === "running",
      ).length;
      const containerCount = vms.filter((v) => v.type === "lxc").length;
      const containersRunning = vms.filter(
        (v) => v.type === "lxc" && v.status === "running",
      ).length;
      // Deduplicate shared storage — shared pools appear once per node but
      // represent the same underlying capacity.  Count each shared pool only once.
      const seen = new Set<string>();
      let storageTotalBytes = 0;
      let storageUsedBytes = 0;
      for (const s of storage) {
        if (s.shared) {
          if (seen.has(s.storage)) continue;
          seen.add(s.storage);
        }
        storageTotalBytes += s.total;
        storageUsedBytes += s.used;
      }

      totalNodes += nodes.length;
      totalNodesOnline += nodesOnline;
      totalVMs += vmCount;
      totalVMsRunning += vmsRunning;
      totalContainers += containerCount;
      totalContainersRunning += containersRunning;
      totalStorageBytes += storageTotalBytes;
      totalStorageUsedBytes += storageUsedBytes;

      for (const vm of vms) {
        vmNameMap.set(vm.id, vm.name);
      }

      return {
        cluster,
        nodeCount: nodes.length,
        nodesOnline,
        vmCount,
        vmsRunning,
        containerCount,
        containersRunning,
        storageTotalBytes,
        storageUsedBytes,
      };
    });

    data = {
      clusters: clusterSummaries,
      totalNodes,
      totalNodesOnline,
      totalVMs,
      totalVMsRunning,
      totalContainers,
      totalContainersRunning,
      totalStorageBytes,
      totalStorageUsedBytes,
      vmNameMap,
    };
  }

  return { data, isLoading, error };
}

export function useCreateCluster() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateClusterRequest) =>
      apiClient.post<CreateClusterResponse>("/api/v1/clusters", req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["clusters"] });
    },
  });
}

export interface UpdateClusterRequest {
  name?: string;
  api_url?: string;
  token_id?: string;
  token_secret?: string;
  tls_fingerprint?: string;
  allow_private_address?: boolean;
}

export function useUpdateCluster() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: UpdateClusterRequest }) =>
      apiClient.put<UpdateClusterResponse>(`/api/v1/clusters/${id}`, body),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["clusters"] });
    },
  });
}

export function useDeleteCluster() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete<{ status: string }>(`/api/v1/clusters/${id}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["clusters"] });
    },
  });
}
