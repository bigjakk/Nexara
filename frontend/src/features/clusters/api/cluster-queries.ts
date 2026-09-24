import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiFetch } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import {
  onRuleWriteError,
  requireRuleDigest,
} from "@/features/networks/api/firewall-rule-digest";
import type {
  ClusterResponse,
  NodeResponse,
  NodeDiskResponse,
  NodeNetworkInterfaceResponse,
  NodePCIDeviceResponse,
  StorageResponse,
  VMResponse,
} from "@/types/api";

export function useCluster(id: string) {
  return useQuery({
    queryKey: ["clusters", id],
    queryFn: () =>
      apiClient.get<ClusterResponse>(apiPath`/api/v1/clusters/${id}`),
    enabled: id.length > 0,
  });
}

export function useClusterNodes(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes"],
    queryFn: () =>
      apiClient.list<NodeResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useClusterStorage(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "storage"],
    queryFn: () =>
      apiClient.list<StorageResponse>(
        apiPath`/api/v1/clusters/${clusterId}/storage`,
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
      apiClient.list<BridgeResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/bridges`,
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
      apiClient.list<MachineTypeResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/machine-types`,
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
      apiClient.list<CPUModelResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/cpu-models`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
    staleTime: 300_000,
  });
}

export interface CPUFlagResponse {
  name: string;
  description?: string;
  /**
   * Cluster nodes on which the flag is usable. An empty array means Proxmox
   * checked and no node supports it; null means Proxmox did not report it.
   */
  supported_on: string[] | null;
}

export function useCPUFlags(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "cpu-flags"],
    queryFn: () =>
      apiClient.list<CPUFlagResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/cpu-flags`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
    staleTime: 300_000,
  });
}

export function useNodeDisks(clusterId: string, nodeId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeId, "disks"],
    queryFn: () =>
      apiClient.list<NodeDiskResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeId}/disks`,
      ),
    enabled: clusterId.length > 0 && nodeId.length > 0,
  });
}

export function useNodeNetworkInterfaces(clusterId: string, nodeId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeId, "network-interfaces"],
    queryFn: () =>
      apiClient.list<NodeNetworkInterfaceResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeId}/network-interfaces`,
      ),
    enabled: clusterId.length > 0 && nodeId.length > 0,
  });
}

export function useNodePCIDevices(clusterId: string, nodeId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeId, "pci-devices"],
    queryFn: () =>
      apiClient.list<NodePCIDeviceResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeId}/pci-devices`,
      ),
    enabled: clusterId.length > 0 && nodeId.length > 0,
  });
}

// --- Node Disk Management ---

export interface DiskSMARTResponse {
  health: string;
  type: string;
  text: string;
  attributes?: SMARTAttributeResponse[];
}

export interface SMARTAttributeResponse {
  id: number;
  name: string;
  value: number;
  worst: number;
  threshold: number;
  raw: string;
  flags: string;
}

export interface ZFSPoolResponse {
  name: string;
  size: number;
  alloc: number;
  free: number;
  frag: number;
  dedup: number;
  health: string;
}

export interface LVMVolumeGroupResponse {
  name: string;
  size: number;
  free: number;
  pv_count: number;
  lv_count: number;
}

export interface LVMThinPoolResponse {
  lv: string;
  vg: string;
  lv_size: number;
  used: number;
  metadata_size: number;
  metadata_used: number;
  data_percent: number;
}

export interface LiveDiskResponse {
  dev_path: string;
  model: string;
  serial: string;
  size: number;
  disk_type: string;
  health: string;
  wearout: string;
  gpt: number;
  used: string;
}

export function useLiveDisks(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "live"],
    queryFn: () =>
      apiClient.list<LiveDiskResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/list`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useDiskSMART(
  clusterId: string,
  nodeName: string,
  disk: string,
) {
  return useQuery({
    queryKey: [
      "clusters",
      clusterId,
      "nodes",
      nodeName,
      "disks",
      "smart",
      disk,
    ],
    queryFn: () =>
      apiClient.get<DiskSMARTResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/smart?disk=${disk}`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0 && disk.length > 0,
  });
}

export function useNodeZFSPools(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "zfs"],
    queryFn: () =>
      apiClient.list<ZFSPoolResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/zfs`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useCreateZFSPool(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: {
      name: string;
      raidlevel: string;
      devices: string;
      compression?: string;
      ashift?: number;
    }) =>
      apiClient.post<{ status: string; upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/zfs`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "zfs"],
      });
    },
  });
}

export function useDeleteZFSPool(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: {
      poolName: string;
      cleanupDisks?: boolean;
      cleanupConfig?: boolean;
    }) => {
      const qp = new URLSearchParams();
      if (params.cleanupDisks) qp.set("cleanup-disks", "true");
      if (params.cleanupConfig) qp.set("cleanup-config", "true");
      return apiClient.delete<{ status: string; upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/zfs/${params.poolName}?${qp}`,
      );
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "zfs"],
      });
    },
  });
}

export function useNodeLVM(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "lvm"],
    queryFn: () =>
      apiClient.list<LVMVolumeGroupResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/lvm`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useCreateLVM(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: {
      name: string;
      device: string;
      add_storage?: boolean;
    }) =>
      apiClient.post<{ status: string; upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/lvm`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "lvm"],
      });
    },
  });
}

export function useDeleteLVM(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: {
      name: string;
      cleanupDisks?: boolean;
      cleanupConfig?: boolean;
    }) => {
      const qp = new URLSearchParams();
      if (params.cleanupDisks) qp.set("cleanup-disks", "true");
      if (params.cleanupConfig) qp.set("cleanup-config", "true");
      return apiClient.delete<{ status: string; upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/lvm/${params.name}?${qp}`,
      );
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "lvm"],
      });
    },
  });
}

export function useNodeLVMThin(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "lvmthin"],
    queryFn: () =>
      apiClient.list<LVMThinPoolResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/lvmthin`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useCreateLVMThin(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: {
      name: string;
      device: string;
      add_storage?: boolean;
    }) =>
      apiClient.post<{ status: string; upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/lvmthin`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: [
          "clusters",
          clusterId,
          "nodes",
          nodeName,
          "disks",
          "lvmthin",
        ],
      });
    },
  });
}

export function useDeleteLVMThin(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: {
      lv: string;
      vg: string;
      cleanupDisks?: boolean;
      cleanupConfig?: boolean;
    }) => {
      const qp = new URLSearchParams();
      qp.set("volume-group", params.vg);
      if (params.cleanupDisks) qp.set("cleanup-disks", "true");
      if (params.cleanupConfig) qp.set("cleanup-config", "true");
      return apiClient.delete<{ status: string; upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/lvmthin/${params.lv}?${qp}`,
      );
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: [
          "clusters",
          clusterId,
          "nodes",
          nodeName,
          "disks",
          "lvmthin",
        ],
      });
    },
  });
}

export interface DirectoryEntryResponse {
  path: string;
  device: string;
  type: string;
  options: string;
  unitfile: string;
}

export function useNodeDirectories(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "directory"],
    queryFn: () =>
      apiClient.list<DirectoryEntryResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/directory`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useCreateDirectory(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: {
      name: string;
      device: string;
      filesystem: string;
      add_storage?: boolean | undefined;
    }) =>
      apiClient.post<{ status: string; upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/directory`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "disks"],
      });
    },
  });
}

export function useInitializeGPT(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (disk: string) =>
      apiClient.post<{ status: string; upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/initgpt`,
        { disk },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes"],
      });
    },
  });
}

export function useWipeDisk(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (disk: string) =>
      apiClient.put<{ status: string; upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/disks/wipe`,
        { disk },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes"],
      });
    },
  });
}

// --- Node Bulk Operations ---

export interface EvacuateMigration {
  vmid: number;
  name: string;
  type: string;
  target_node: string;
  upid: string;
  error?: string;
}

export interface EvacuateResponse {
  status: string;
  migrations: EvacuateMigration[];
  message?: string;
}

export function useEvacuateNode(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: { target_node?: string | undefined }) =>
      apiClient.post<EvacuateResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/evacuate`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId],
      });
    },
  });
}

// --- Node Firewall ---

export interface NodeFirewallRuleResponse {
  pos: number;
  type: string;
  action: string;
  source?: string;
  dest?: string;
  sport?: string;
  dport?: string;
  proto?: string;
  enable: number;
  comment?: string;
  macro?: string;
  log?: string;
  iface?: string;
  /** Digest of the whole rule list; see FirewallRule.digest. */
  digest: string;
}

export interface FirewallLogEntryResponse {
  n: number;
  t: string;
}

export function useNodeFirewallRules(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "firewall", "rules"],
    queryFn: () =>
      apiClient.list<NodeFirewallRuleResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/firewall/rules`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useCreateNodeFirewallRule(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (rule: Omit<NodeFirewallRuleResponse, "pos" | "digest">) =>
      apiClient.post<{ status: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/firewall/rules`,
        rule,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: [
          "clusters",
          clusterId,
          "nodes",
          nodeName,
          "firewall",
          "rules",
        ],
      });
    },
  });
}

export function useDeleteNodeFirewallRule(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  const reload = () =>
    queryClient.invalidateQueries({
      queryKey: ["clusters", clusterId, "nodes", nodeName, "firewall", "rules"],
    });
  return useMutation({
    // digest is the rule list's, as the row was read — so Proxmox refuses the
    // delete (409) rather than remove another rule if the list has changed.
    mutationFn: ({ pos, digest }: { pos: number; digest: string }) =>
      apiClient.delete<{ status: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/firewall/rules/${pos}?digest=${requireRuleDigest(digest)}`,
      ),
    // Returned, so the mutation stays pending — and the table's Delete
    // buttons disabled — until the list is refetched: a rule is deleted by
    // position, and a position read from the stale list names another rule.
    onSuccess: reload,
    onError: (error) =>
      onRuleWriteError(
        error,
        "Nothing was deleted",
        `node ${nodeName}`,
        reload,
      ),
  });
}

export function useNodeFirewallLog(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "firewall", "log"],
    queryFn: () =>
      apiClient.list<FirewallLogEntryResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/firewall/log?limit=500`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

// --- Node Services ---

export interface NodeServiceResponse {
  service: string;
  name: string;
  desc: string;
  state: string;
}

export function useNodeServices(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "services"],
    queryFn: () =>
      apiClient.list<NodeServiceResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/services`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useServiceAction(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ service, action }: { service: string; action: string }) =>
      apiClient.post<{ status: string; upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/services/${service}/${action}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "services"],
      });
    },
  });
}

// --- Node Syslog ---

export interface SyslogEntryResponse {
  n: number;
  t: string;
}

export function useNodeSyslog(
  clusterId: string,
  nodeName: string,
  params?: {
    start?: number | undefined;
    limit?: number | undefined;
    service?: string | undefined;
    since?: string | undefined;
    until?: string | undefined;
  },
) {
  const searchParams = new URLSearchParams();
  if (params?.start !== undefined)
    searchParams.set("start", String(params.start));
  if (params?.limit !== undefined)
    searchParams.set("limit", String(params.limit));
  if (params?.service) searchParams.set("service", params.service);
  if (params?.since) searchParams.set("since", params.since);
  if (params?.until) searchParams.set("until", params.until);

  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "syslog", params],
    queryFn: () =>
      apiClient.page<SyslogEntryResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/syslog?${searchParams}`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
    staleTime: 30_000,
    placeholderData: (prev) => prev,
  });
}

// --- Node DNS/Time/Power Management ---

export interface NodeDNSResponse {
  search: string;
  dns1: string;
  dns2: string;
  dns3: string;
}

export interface NodeTimeResponse {
  timezone: string;
  time: number;
  localtime: number;
}

export function useNodeDNS(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "dns"],
    queryFn: () =>
      apiClient.get<NodeDNSResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/dns`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useSetNodeDNS(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: {
      search: string;
      dns1: string;
      dns2: string;
      dns3: string;
    }) =>
      apiClient.put<{ status: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/dns`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "dns"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes"],
      });
    },
  });
}

export function useNodeTime(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "time"],
    queryFn: () =>
      apiClient.get<NodeTimeResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/time`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useSetNodeTimezone(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: { timezone: string }) =>
      apiClient.put<{ status: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/time`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "time"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes"],
      });
    },
  });
}

export function useShutdownNode(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiClient.post<{ status: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/shutdown`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes"],
      });
    },
  });
}

export function useRebootNode(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiClient.post<{ status: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/reboot`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes"],
      });
    },
  });
}

/** Enter (enable=true) or exit (enable=false) HA node maintenance via SSH. */
export function useSetNodeMaintenance(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (enable: boolean) =>
      apiClient.post<{ status: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/maintenance`,
        { enable },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes"],
      });
    },
  });
}

export function useClusterVMs(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "vms"],
    queryFn: () =>
      apiClient.list<VMResponse>(apiPath`/api/v1/clusters/${clusterId}/vms`),
    enabled: clusterId.length > 0,
    refetchInterval: 60_000, // WS events handle immediate updates
  });
}

/** Result of asking the server to re-verify and re-pin a cluster certificate. */
export interface VerifyCertificateResult {
  fingerprint: string;
  updated: boolean;
  message: string;
}

/**
 * Re-pins a cluster to the certificate its endpoint currently serves.
 *
 * The server refuses unless a live TLS handshake and the cluster's own report
 * of that node agree, so this is a one-click repair for a rotated certificate
 * rather than a blanket "trust whatever is there" — see VerifyCertificate in
 * internal/api/handlers/clusters.go.
 */
export function useVerifyClusterCertificate(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiClient.post<VerifyCertificateResult>(
        apiPath`/api/v1/clusters/${clusterId}/verify-certificate`,
        {},
      ),
    onSuccess: () => {
      // The cluster row carries the health issue that drives the banner, so
      // refetching is what makes the warning disappear once it is repaired.
      void queryClient.invalidateQueries({ queryKey: ["clusters"] });
    },
  });
}

/**
 * Filename the server asked us to save a download as.
 *
 * Only the plain `filename="..."` form is read, because that is the only form
 * our handlers emit. Anything else falls back to the caller's own name rather
 * than letting a response header pick a name on disk — the server already
 * sanitises it, and this is the cheap second check.
 */
function filenameFromDisposition(header: string | null): string {
  if (!header) return "";
  const name = /filename="([^"]+)"/.exec(header)?.[1] ?? "";
  if (
    !name ||
    name.includes("/") ||
    name.includes("\\") ||
    name.includes("..")
  ) {
    return "";
  }
  return name;
}

/**
 * Downloads a node's `pvereport` support bundle and hands it to the browser.
 *
 * A mutation rather than a query: it is an imperative action with a side
 * effect (a file lands in Downloads), it is expensive enough that Proxmox
 * takes tens of seconds to assemble one, and caching the result would pin
 * megabytes of text in memory for no benefit.
 *
 * apiClient is bypassed deliberately — it decodes JSON, and this endpoint
 * answers with plain text plus the Content-Disposition that names the file.
 */
export function useDownloadNodeReport(clusterId: string, nodeName: string) {
  return useMutation({
    mutationFn: async () => {
      const res = await apiFetch(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/report`,
        { credentials: "same-origin" },
      );
      if (!res.ok) {
        throw new Error(
          res.status === 403
            ? "You do not have permission to download this report."
            : `Failed to generate the report (HTTP ${String(res.status)}).`,
        );
      }

      const blob = await res.blob();
      const link = document.createElement("a");
      link.href = URL.createObjectURL(blob);
      link.download =
        filenameFromDisposition(res.headers.get("Content-Disposition")) ||
        `nexara-report-${nodeName}.txt`;
      link.click();
      URL.revokeObjectURL(link.href);
    },
  });
}

/** One temperature the node's kernel exposes, as read from its hwmon tree. */
export interface NodeSensorReading {
  /** hwmon chip name, e.g. "coretemp" or "nvme". */
  chip: string;
  /** hwmon directory, e.g. "hwmon2" — the only thing separating two devices
   * that share a chip name, which is normal for multiple NVMe drives. */
  device: string;
  /** Stable identity within the device, e.g. "temp1". Shown when the kernel
   * publishes no label for the sensor. */
  key: string;
  label: string;
  temp_c: number;
  /** tempN_max, when the driver publishes a usable one. */
  high_c?: number;
  /** tempN_crit, when the driver publishes a usable one. */
  crit_c?: number;
}

export interface NodeSensorsResponse {
  items: NodeSensorReading[];
  total: number;
  /** False when the node could not be read at all; `reason` then says why and
   * what to do about it. True with an empty `items` is the other outcome — the
   * node was read and simply exposes no temperature sensors. */
  available: boolean;
  reason?: string;
}

/**
 * Reads a node's hardware temperatures.
 *
 * apiClient.get, not .list: the payload is the standard {items,total} envelope
 * plus the availability fields, and .list would throw those away.
 *
 * The poll matches the server's 60s cache exactly, so an open node page costs
 * at most one SSH session per minute however many people are watching it.
 * TanStack pauses the interval when the tab loses focus, which is what keeps a
 * forgotten background tab from doing this all night.
 */
export function useNodeSensors(
  clusterId: string,
  nodeName: string,
  enabled = true,
) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "sensors"],
    queryFn: () =>
      apiClient.get<NodeSensorsResponse>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/sensors`,
      ),
    enabled: enabled && clusterId.length > 0 && nodeName.length > 0,
    staleTime: 60_000,
    refetchInterval: 60_000,
  });
}
