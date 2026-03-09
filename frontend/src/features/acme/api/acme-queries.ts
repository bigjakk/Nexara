import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

export interface ACMEAccount {
  name?: string;
  account?: unknown;
  directory?: string;
  location?: string;
  tos?: string;
  [key: string]: unknown;
}

export interface ACMEPlugin {
  plugin: string;
  type: string;
  api?: string;
  data?: string;
  [key: string]: unknown;
}

export interface ACMEChallengeSchemaField {
  description?: string;
  type?: string;
  optional?: number;
  default?: unknown;
  [key: string]: unknown;
}

export interface ACMEChallengeSchemaDetail {
  name?: string;
  description?: string;
  fields?: Record<string, ACMEChallengeSchemaField>;
  [key: string]: unknown;
}

export interface ACMEChallengeSchema {
  id: string;
  name: string;
  type: string;
  schema?: ACMEChallengeSchemaDetail;
  [key: string]: unknown;
}

export interface ACMEDirectory {
  name: string;
  url: string;
  [key: string]: unknown;
}

export interface NodeCertificate {
  filename?: string;
  fingerprint?: string;
  issuer?: string;
  notafter?: number;
  notbefore?: number;
  subject?: string;
  san?: string;
  [key: string]: unknown;
}

export function useACMEAccounts(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "acme", "accounts"],
    queryFn: () => apiClient.get<ACMEAccount[]>(`/api/v1/clusters/${clusterId}/acme/accounts`),
    enabled: clusterId.length > 0,
  });
}

export function useCreateACMEAccount(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { name?: string; contact: string; directory?: string; tos_url?: string }) =>
      apiClient.post<{ upid: string }>(`/api/v1/clusters/${clusterId}/acme/accounts`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "acme"] }); },
  });
}

export function useDeleteACMEAccount(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/acme/accounts/${encodeURIComponent(name)}`),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "acme"] }); },
  });
}

export function useACMEPlugins(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "acme", "plugins"],
    queryFn: () => apiClient.get<ACMEPlugin[]>(`/api/v1/clusters/${clusterId}/acme/plugins`),
    enabled: clusterId.length > 0,
  });
}

export function useCreateACMEPlugin(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { id: string; type: string; api?: string; data?: string; "validation-delay"?: number }) =>
      apiClient.post(`/api/v1/clusters/${clusterId}/acme/plugins`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "acme"] }); },
  });
}

export function useDeleteACMEPlugin(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/acme/plugins/${encodeURIComponent(id)}`),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "acme"] }); },
  });
}

export function useACMEChallengeSchema(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "acme", "challenge-schema"],
    queryFn: () => apiClient.get<ACMEChallengeSchema[]>(`/api/v1/clusters/${clusterId}/acme/challenge-schema`),
    enabled: clusterId.length > 0,
  });
}

export function useACMETOS(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "acme", "tos"],
    queryFn: () => apiClient.get<{ url: string }>(`/api/v1/clusters/${clusterId}/acme/tos`),
    enabled: clusterId.length > 0,
  });
}

export function useACMEDirectories(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "acme", "directories"],
    queryFn: () => apiClient.get<ACMEDirectory[]>(`/api/v1/clusters/${clusterId}/acme/directories`),
    enabled: clusterId.length > 0,
  });
}

export interface NodeACMEConfig {
  acme?: string;
  acmedomain0?: string;
  acmedomain1?: string;
  acmedomain2?: string;
  acmedomain3?: string;
  acmedomain4?: string;
  acmedomain5?: string;
  [key: string]: unknown;
}

export function useNodeACMEConfig(clusterId: string, node: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", node, "acme-config"],
    queryFn: () => apiClient.get<NodeACMEConfig>(`/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(node)}/acme-config`),
    enabled: clusterId.length > 0 && node.length > 0,
  });
}

export function useSetNodeACMEConfig(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ node, config }: { node: string; config: NodeACMEConfig }) =>
      apiClient.put(`/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(node)}/acme-config`, config),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "nodes", vars.node] });
    },
  });
}

export function useNodeCertificates(clusterId: string, node: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", node, "certificates"],
    queryFn: () => apiClient.get<NodeCertificate[]>(`/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(node)}/certificates`),
    enabled: clusterId.length > 0 && node.length > 0,
  });
}

export function useOrderNodeCertificate(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ node, force }: { node: string; force?: boolean }) =>
      apiClient.post<{ upid: string }>(`/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(node)}/certificates/order`, { force }),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId] }); },
  });
}

export function useRenewNodeCertificate(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ node, force }: { node: string; force?: boolean }) =>
      apiClient.put<{ upid: string }>(`/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(node)}/certificates/renew`, { force }),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId] }); },
  });
}
