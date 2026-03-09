import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { ClusterResponse, NodeResponse, StorageResponse, VMResponse } from "@/types/api";

export function useCluster(id: string) {
  return useQuery({
    queryKey: ["clusters", id],
    queryFn: () => apiClient.get<ClusterResponse>(`/api/v1/clusters/${id}`),
    enabled: id.length > 0,
  });
}

export function useClusterNodes(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes"],
    queryFn: () =>
      apiClient.get<NodeResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useClusterStorage(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "storage"],
    queryFn: () =>
      apiClient.get<StorageResponse[]>(
        `/api/v1/clusters/${clusterId}/storage`,
      ),
    enabled: clusterId.length > 0,
  });
}

export interface BridgeResponse {
  iface: string;
  active: boolean;
  address?: string;
  cidr?: string;
}

export function useNodeBridges(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "bridges"],
    queryFn: () =>
      apiClient.get<BridgeResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/bridges`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export interface MachineTypeResponse {
  id: string;
  type: string;
}

export function useMachineTypes(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "machine-types"],
    queryFn: () =>
      apiClient.get<MachineTypeResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/machine-types`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
    staleTime: 300_000,
  });
}

export interface CPUModelResponse {
  name: string;
  vendor: string;
  custom: boolean;
}

export function useCPUModels(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "cpu-models"],
    queryFn: () =>
      apiClient.get<CPUModelResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/cpu-models`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
    staleTime: 300_000,
  });
}

export function useClusterVMs(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "vms"],
    queryFn: () =>
      apiClient.get<VMResponse[]>(
        `/api/v1/clusters/${clusterId}/vms`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: 60_000, // WS events handle immediate updates
  });
}
