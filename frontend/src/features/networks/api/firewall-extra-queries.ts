import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

export interface FirewallAlias {
  name: string;
  cidr: string;
  comment?: string;
  [key: string]: unknown;
}

export interface FirewallIPSet {
  name: string;
  comment?: string;
  [key: string]: unknown;
}

export interface FirewallIPSetEntry {
  cidr: string;
  nomatch?: number;
  comment?: string;
  [key: string]: unknown;
}

export interface FirewallSecurityGroup {
  group: string;
  comment?: string;
  [key: string]: unknown;
}

export interface FirewallLogEntry {
  n: number;
  t: string;
  [key: string]: unknown;
}

// Aliases
export function useFirewallAliases(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "firewall", "aliases"],
    queryFn: () => apiClient.get<FirewallAlias[]>(`/api/v1/clusters/${clusterId}/firewall/aliases`),
    enabled: clusterId.length > 0,
  });
}

export function useCreateFirewallAlias(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { name: string; cidr: string; comment?: string }) =>
      apiClient.post(`/api/v1/clusters/${clusterId}/firewall/aliases`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "firewall", "aliases"] }); },
  });
}

export function useDeleteFirewallAlias(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/firewall/aliases/${encodeURIComponent(name)}`),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "firewall", "aliases"] }); },
  });
}

// IP Sets
export function useFirewallIPSets(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "firewall", "ipset"],
    queryFn: () => apiClient.get<FirewallIPSet[]>(`/api/v1/clusters/${clusterId}/firewall/ipset`),
    enabled: clusterId.length > 0,
  });
}

export function useCreateFirewallIPSet(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { name: string; comment?: string }) =>
      apiClient.post(`/api/v1/clusters/${clusterId}/firewall/ipset`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "firewall", "ipset"] }); },
  });
}

export function useDeleteFirewallIPSet(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/firewall/ipset/${encodeURIComponent(name)}`),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "firewall", "ipset"] }); },
  });
}

export function useFirewallIPSetEntries(clusterId: string, setName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "firewall", "ipset", setName, "entries"],
    queryFn: () => apiClient.get<FirewallIPSetEntry[]>(`/api/v1/clusters/${clusterId}/firewall/ipset/${encodeURIComponent(setName)}/entries`),
    enabled: clusterId.length > 0 && setName.length > 0,
  });
}

export function useAddFirewallIPSetEntry(clusterId: string, setName: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { cidr: string; nomatch?: number; comment?: string }) =>
      apiClient.post(`/api/v1/clusters/${clusterId}/firewall/ipset/${encodeURIComponent(setName)}/entries`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "firewall", "ipset", setName] }); },
  });
}

export function useDeleteFirewallIPSetEntry(clusterId: string, setName: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (cidr: string) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/firewall/ipset/${encodeURIComponent(setName)}/entries/${encodeURIComponent(cidr)}`),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "firewall", "ipset", setName] }); },
  });
}

// Security Groups
export function useFirewallSecurityGroups(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "firewall", "groups"],
    queryFn: () => apiClient.get<FirewallSecurityGroup[]>(`/api/v1/clusters/${clusterId}/firewall/groups`),
    enabled: clusterId.length > 0,
  });
}

export function useCreateFirewallSecurityGroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { group: string; comment?: string }) =>
      apiClient.post(`/api/v1/clusters/${clusterId}/firewall/groups`, data),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "firewall", "groups"] }); },
  });
}

export function useDeleteFirewallSecurityGroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (group: string) =>
      apiClient.delete(`/api/v1/clusters/${clusterId}/firewall/groups/${encodeURIComponent(group)}`),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "firewall", "groups"] }); },
  });
}

// Firewall Log
export function useFirewallLog(clusterId: string, node: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "firewall", "log", node],
    queryFn: () => apiClient.get<FirewallLogEntry[]>(`/api/v1/clusters/${clusterId}/firewall/log?node=${encodeURIComponent(node)}`),
    enabled: clusterId.length > 0 && node.length > 0,
    refetchInterval: 10_000,
  });
}
