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
    mutationFn: (data: { id: string; type: string; api?: string; data?: string }) =>
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

export function useACMEDirectories(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "acme", "directories"],
    queryFn: () => apiClient.get<ACMEDirectory[]>(`/api/v1/clusters/${clusterId}/acme/directories`),
    enabled: clusterId.length > 0,
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
