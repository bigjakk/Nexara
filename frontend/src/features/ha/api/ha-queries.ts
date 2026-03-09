import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

export interface HAResource {
  sid: string;
  type: string;
  state: string;
  group: string;
  status: string;
  max_relocate: number;
  [key: string]: unknown;
}

export interface HAGroup {
  group: string;
  nodes: string;
  restricted: number;
  nofailback: number;
  [key: string]: unknown;
}

export interface HAStatusEntry {
  id: string;
  type: string;
  node?: string;
  status: string;
  state?: string;
  crm_state?: string;
  quorum?: number;
  timestamp?: number;
  request_state?: string;
  sid?: string;
  [key: string]: unknown;
}

export interface CreateHAResourceRequest {
  sid: string;
  state?: string;
  group?: string;
  max_restart?: number;
  max_relocate?: number;
  comment?: string;
}

export interface UpdateHAResourceRequest {
  state?: string;
  group?: string;
  max_restart?: number;
  max_relocate?: number;
  comment?: string;
}

export interface CreateHAGroupRequest {
  group: string;
  nodes: string;
  restricted?: number;
  nofailback?: number;
  comment?: string;
}

export interface UpdateHAGroupRequest {
  nodes?: string;
  restricted?: number;
  nofailback?: number;
  comment?: string;
}

export function useHAResources(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ha", "resources"],
    queryFn: () => apiClient.get<HAResource[]>(`/api/v1/clusters/${clusterId}/ha/resources`),
    enabled: clusterId.length > 0,
  });
}

export function useCreateHAResource(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateHAResourceRequest) =>
      apiClient.post(`/api/v1/clusters/${clusterId}/ha/resources`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] }); },
  });
}

export function useUpdateHAResource(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ sid, ...data }: UpdateHAResourceRequest & { sid: string }) =>
      apiClient.put(`/api/v1/clusters/${clusterId}/ha/resources/${encodeURIComponent(sid)}`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] }); },
  });
}

export function useDeleteHAResource(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (sid: string) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/ha/resources/${encodeURIComponent(sid)}`),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] }); },
  });
}

export function useHAGroups(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ha", "groups"],
    queryFn: () => apiClient.get<HAGroup[]>(`/api/v1/clusters/${clusterId}/ha/groups`),
    enabled: clusterId.length > 0,
  });
}

export function useCreateHAGroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateHAGroupRequest) =>
      apiClient.post(`/api/v1/clusters/${clusterId}/ha/groups`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] }); },
  });
}

export function useUpdateHAGroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ group, ...data }: UpdateHAGroupRequest & { group: string }) =>
      apiClient.put(`/api/v1/clusters/${clusterId}/ha/groups/${encodeURIComponent(group)}`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] }); },
  });
}

export function useDeleteHAGroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (group: string) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/ha/groups/${encodeURIComponent(group)}`),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] }); },
  });
}

export function useHAStatus(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ha", "status"],
    queryFn: () => apiClient.get<HAStatusEntry[]>(`/api/v1/clusters/${clusterId}/ha/status`),
    enabled: clusterId.length > 0,
    refetchInterval: 30_000,
  });
}
