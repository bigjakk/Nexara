import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

export interface ClusterOptions {
  console?: string;
  keyboard?: string;
  language?: string;
  email_from?: string;
  http_proxy?: string;
  mac_prefix?: string;
  migration?: string;
  migration_type?: string;
  bwlimit?: string;
  "next-id"?: string;
  ha?: string;
  fencing?: string;
  crs?: string;
  max_workers?: number;
  description?: string;
  "registered-tags"?: string;
  "user-tag-access"?: string;
  "tag-style"?: string;
  digest?: string;
  [key: string]: unknown;
}

export interface TagsResponse {
  registered_tags: string;
  user_tag_access: string;
  tag_style: string;
  [key: string]: unknown;
}

export interface ClusterConfig {
  nodes?: CorosyncNode[];
  totem?: unknown;
  version?: number;
  [key: string]: unknown;
}

export interface ClusterJoinInfo {
  config_digest?: string;
  fingerprint?: string;
  nodelist?: CorosyncNode[];
  totem?: unknown;
  [key: string]: unknown;
}

export interface CorosyncNode {
  name: string;
  nodeid?: number;
  pve_addr?: string;
  pve_fp?: string;
  quorum_votes?: number;
  ring0_addr?: string;
  ring1_addr?: string;
  [key: string]: unknown;
}

export function useClusterOptions(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "options"],
    queryFn: () => apiClient.get<ClusterOptions>(`/api/v1/clusters/${clusterId}/options`),
    enabled: clusterId.length > 0,
  });
}

export function useUpdateClusterOptions(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<ClusterOptions>) =>
      apiClient.put<{ status: string }>(`/api/v1/clusters/${clusterId}/options`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "options"] });
    },
  });
}

export function useClusterDescription(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "description"],
    queryFn: () => apiClient.get<{ description: string }>(`/api/v1/clusters/${clusterId}/description`),
    enabled: clusterId.length > 0,
  });
}

export function useUpdateClusterDescription(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (description: string) =>
      apiClient.put<{ status: string }>(`/api/v1/clusters/${clusterId}/description`, { description }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "description"] });
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "options"] });
    },
  });
}

export function useClusterTags(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "tags"],
    queryFn: () => apiClient.get<TagsResponse>(`/api/v1/clusters/${clusterId}/tags`),
    enabled: clusterId.length > 0,
  });
}

export function useUpdateClusterTags(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { registered_tags?: string; user_tag_access?: string; tag_style?: string }) =>
      apiClient.put<{ status: string }>(`/api/v1/clusters/${clusterId}/tags`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "tags"] });
    },
  });
}

export function useClusterConfig(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "config"],
    queryFn: () => apiClient.get<ClusterConfig>(`/api/v1/clusters/${clusterId}/config`),
    enabled: clusterId.length > 0,
  });
}

export function useClusterJoinInfo(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "config", "join"],
    queryFn: () => apiClient.get<ClusterJoinInfo>(`/api/v1/clusters/${clusterId}/config/join`),
    enabled: clusterId.length > 0,
  });
}

export function useCorosyncNodes(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "config", "nodes"],
    queryFn: () => apiClient.get<CorosyncNode[]>(`/api/v1/clusters/${clusterId}/config/nodes`),
    enabled: clusterId.length > 0,
  });
}
