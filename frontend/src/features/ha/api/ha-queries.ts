import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { isPVEAtLeast, PVE_FEATURES } from "@/lib/pve-version";

/**
 * An HA resource as the API returns it: Proxmox's raw resource config, where a
 * property left at its default is ABSENT rather than filled in (see
 * proxmox.HAResource in internal/proxmox/types.go). Read an unset one as
 * Proxmox's default, HA_RESOURCE_DEFAULTS — never as 0.
 */
export interface HAResource {
  sid: string;
  type: string;
  /** "" when the resource sets none, which Proxmox runs as "started". */
  state: string;
  group: string;
  status: string;
  max_relocate?: number;
  max_restart?: number;
  comment?: string;
  /** 0 or 1. */
  failback?: number;
  [key: string]: unknown;
}

/**
 * Proxmox's defaults for the HA resource properties a config may leave unset.
 * pve-ha-manager declares them in src/PVE/HA/Resources.pm's propertyList, and
 * fills them in only through checked_resources_config (src/PVE/HA/Config.pm),
 * which the HA stack and its status and rules endpoints read — never the two
 * resource reads this app makes, which return the raw config.
 */
export const HA_RESOURCE_DEFAULTS: Readonly<{
  state: string;
  max_restart: number;
  max_relocate: number;
  failback: number;
}> = {
  state: "started",
  max_restart: 1,
  max_relocate: 1,
  failback: 1,
};

/**
 * Whether an HA resource has a failback of its own on a cluster running this
 * Proxmox VE version. It arrived with HA affinity rules in PVE 9
 * (pve-ha-manager 5.0.2), taking over from a group's nofailback. PVE 8's
 * resource schema has none (pve-ha-manager 4.0.x, e.g. 4.0.7 at 53d8e48,
 * src/PVE/HA/Resources.pm), and its create and update schemas refuse an
 * unknown key, so sending failback to PVE 8 is a 400 — there failback is the
 * HA group's nofailback. An unknown or unparseable version answers false:
 * hiding the switch is safe on PVE 9, while offering it on PVE 8 is a 400.
 */
export function haResourceHasFailback(pveVersion: string): boolean {
  return isPVEAtLeast(pveVersion, PVE_FEATURES.HA_RULES);
}

/**
 * A retry count as a number input holds it, or undefined when it holds no
 * count the form may send. Number(), not parseInt: a number input accepts
 * exponent notation, so "1e1" is a valid 10 to the browser's constraint
 * validation — which is what lets it reach the form — and parseInt read it as
 * 1 ("10e-1" as 10). Blank is undefined rather than Number("")'s 0, and so is
 * anything the input's own constraints refuse: a fraction, a negative, or a
 * count above its ceiling.
 */
export function parseRetryCount(
  text: string,
  ceiling: number,
): number | undefined {
  if (text.trim() === "") return undefined;
  const n = Number(text);
  return Number.isInteger(n) && n >= 0 && n <= ceiling ? n : undefined;
}

export interface HAGroup {
  group: string;
  nodes: string;
  restricted: number;
  nofailback: number;
  comment?: string;
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
  failback?: number;
}

export interface UpdateHAResourceRequest {
  state?: string;
  group?: string;
  max_restart?: number;
  max_relocate?: number;
  comment?: string;
  failback?: number;
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

export interface UpdateHARuleRequest {
  type: string;
  resources?: string;
  nodes?: string;
  strict?: number;
  affinity?: string;
  comment?: string;
  disable?: number;
}

export function useHAResources(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ha", "resources"],
    queryFn: () =>
      apiClient.list<HAResource>(
        apiPath`/api/v1/clusters/${clusterId}/ha/resources`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateHAResource(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateHAResourceRequest) =>
      apiClient.post(apiPath`/api/v1/clusters/${clusterId}/ha/resources`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] });
    },
  });
}

export function useUpdateHAResource(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ sid, ...data }: UpdateHAResourceRequest & { sid: string }) =>
      apiClient.put(
        apiPath`/api/v1/clusters/${clusterId}/ha/resources/${sid}`,
        data,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] });
    },
  });
}

export function useDeleteHAResource(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (sid: string) =>
      apiClient.delete(
        apiPath`/api/v1/clusters/${clusterId}/ha/resources/${sid}`,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] });
    },
  });
}

export function useHAGroups(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ha", "groups"],
    queryFn: () =>
      apiClient.list<HAGroup>(apiPath`/api/v1/clusters/${clusterId}/ha/groups`),
    enabled: clusterId.length > 0,
  });
}

export function useCreateHAGroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateHAGroupRequest) =>
      apiClient.post(apiPath`/api/v1/clusters/${clusterId}/ha/groups`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] });
    },
  });
}

export function useUpdateHAGroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      group,
      ...data
    }: UpdateHAGroupRequest & { group: string }) =>
      apiClient.put(
        apiPath`/api/v1/clusters/${clusterId}/ha/groups/${group}`,
        data,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] });
    },
  });
}

export function useDeleteHAGroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (group: string) =>
      apiClient.delete(
        apiPath`/api/v1/clusters/${clusterId}/ha/groups/${group}`,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] });
    },
  });
}

export interface HARuleEntry {
  rule: string;
  type: string;
  resources: string;
  nodes?: string;
  strict?: number;
  affinity?: string;
  comment?: string;
  disable?: number;
  [key: string]: unknown;
}

export interface CreateHARuleRequest {
  rule: string;
  type: string;
  resources: string;
  nodes?: string;
  strict?: number;
  affinity?: string;
  comment?: string;
}

export function useHARules(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ha", "rules"],
    queryFn: () =>
      apiClient.list<HARuleEntry>(
        apiPath`/api/v1/clusters/${clusterId}/ha/rules`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateHARule(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateHARuleRequest) =>
      apiClient.post(apiPath`/api/v1/clusters/${clusterId}/ha/rules`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] });
    },
  });
}

export function useDeleteHARule(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (rule: string) =>
      apiClient.delete(apiPath`/api/v1/clusters/${clusterId}/ha/rules/${rule}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] });
    },
  });
}

export function useUpdateHARule(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ rule, ...data }: UpdateHARuleRequest & { rule: string }) =>
      apiClient.put(
        apiPath`/api/v1/clusters/${clusterId}/ha/rules/${rule}`,
        data,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] });
    },
  });
}

export function useHAManagerStatus(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ha", "manager-status"],
    queryFn: () =>
      apiClient.get<Record<string, unknown>>(
        apiPath`/api/v1/clusters/${clusterId}/ha/manager-status`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: 30_000,
  });
}

export function useHAStatus(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "ha", "status"],
    queryFn: () =>
      apiClient.list<HAStatusEntry>(
        apiPath`/api/v1/clusters/${clusterId}/ha/status`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: 30_000,
  });
}

/** Re-arm the HA stack cluster-wide (PVE 9.2+). */
export function useArmHA(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiClient.post(apiPath`/api/v1/clusters/${clusterId}/ha/arm`, {}),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] });
    },
  });
}

/** Disarm the HA stack cluster-wide for maintenance (PVE 9.2+). */
export function useDisarmHA(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (resourceMode: "freeze" | "ignore") =>
      apiClient.post(apiPath`/api/v1/clusters/${clusterId}/ha/disarm`, {
        resource_mode: resourceMode,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "ha"] });
    },
  });
}
