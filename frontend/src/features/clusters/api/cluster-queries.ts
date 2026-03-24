import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
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

export function useNodeDisks(clusterId: string, nodeId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeId, "disks"],
    queryFn: () =>
      apiClient.get<NodeDiskResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${nodeId}/disks`,
      ),
    enabled: clusterId.length > 0 && nodeId.length > 0,
  });
}

export function useNodeNetworkInterfaces(clusterId: string, nodeId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeId, "network-interfaces"],
    queryFn: () =>
      apiClient.get<NodeNetworkInterfaceResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${nodeId}/network-interfaces`,
      ),
    enabled: clusterId.length > 0 && nodeId.length > 0,
  });
}

export function useNodePCIDevices(clusterId: string, nodeId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeId, "pci-devices"],
    queryFn: () =>
      apiClient.get<NodePCIDeviceResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${nodeId}/pci-devices`,
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
      apiClient.get<LiveDiskResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/list`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useDiskSMART(clusterId: string, nodeName: string, disk: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "smart", disk],
    queryFn: () =>
      apiClient.get<DiskSMARTResponse>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/smart?disk=${encodeURIComponent(disk)}`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0 && disk.length > 0,
  });
}

export function useNodeZFSPools(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "zfs"],
    queryFn: () =>
      apiClient.get<ZFSPoolResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/zfs`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useCreateZFSPool(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: { name: string; raidlevel: string; devices: string; compression?: string; ashift?: number }) =>
      apiClient.post<{ status: string; upid: string }>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/zfs`,
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
    mutationFn: (params: { poolName: string; cleanupDisks?: boolean; cleanupConfig?: boolean }) => {
      const qp = new URLSearchParams();
      if (params.cleanupDisks) qp.set("cleanup-disks", "true");
      if (params.cleanupConfig) qp.set("cleanup-config", "true");
      const qs = qp.toString();
      return apiClient.delete<{ status: string; upid: string }>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/zfs/${encodeURIComponent(params.poolName)}${qs ? `?${qs}` : ""}`,
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
      apiClient.get<LVMVolumeGroupResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/lvm`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useCreateLVM(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: { name: string; device: string; add_storage?: boolean }) =>
      apiClient.post<{ status: string; upid: string }>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/lvm`,
        params,
      ),
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
      apiClient.get<LVMThinPoolResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/lvmthin`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useCreateLVMThin(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: { name: string; device: string; add_storage?: boolean }) =>
      apiClient.post<{ status: string; upid: string }>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/lvmthin`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "disks", "lvmthin"],
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
      apiClient.get<DirectoryEntryResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/directory`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useCreateDirectory(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: { name: string; device: string; filesystem: string; add_storage?: boolean | undefined }) =>
      apiClient.post<{ status: string; upid: string }>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/directory`,
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
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/initgpt`,
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
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/disks/wipe`,
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
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/evacuate`,
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
}

export interface FirewallLogEntryResponse {
  n: number;
  t: string;
}

export function useNodeFirewallRules(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "firewall", "rules"],
    queryFn: () =>
      apiClient.get<NodeFirewallRuleResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/firewall/rules`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useCreateNodeFirewallRule(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (rule: Omit<NodeFirewallRuleResponse, "pos">) =>
      apiClient.post<{ status: string }>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/firewall/rules`,
        rule,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "firewall", "rules"],
      });
    },
  });
}

export function useDeleteNodeFirewallRule(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (pos: number) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/firewall/rules/${String(pos)}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "firewall", "rules"],
      });
    },
  });
}

export function useNodeFirewallLog(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "firewall", "log"],
    queryFn: () =>
      apiClient.get<FirewallLogEntryResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/firewall/log?limit=500`,
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
      apiClient.get<NodeServiceResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/services`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useServiceAction(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ service, action }: { service: string; action: string }) =>
      apiClient.post<{ status: string; upid: string }>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/services/${encodeURIComponent(service)}/${encodeURIComponent(action)}`,
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

export function useNodeSyslog(clusterId: string, nodeName: string, params?: { start?: number | undefined; limit?: number | undefined; service?: string | undefined }) {
  const searchParams = new URLSearchParams();
  if (params?.start !== undefined) searchParams.set("start", String(params.start));
  if (params?.limit !== undefined) searchParams.set("limit", String(params.limit));
  if (params?.service) searchParams.set("service", params.service);
  const qs = searchParams.toString();

  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", nodeName, "syslog", params],
    queryFn: () =>
      apiClient.get<SyslogEntryResponse[]>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/syslog${qs ? `?${qs}` : ""}`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
    retry: 1,
    staleTime: 30_000,
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
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/dns`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useSetNodeDNS(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: { search: string; dns1: string; dns2: string; dns3: string }) =>
      apiClient.put<{ status: string }>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/dns`,
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
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/time`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useSetNodeTimezone(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: { timezone: string }) =>
      apiClient.put<{ status: string }>(
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/time`,
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
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/shutdown`,
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
        `/api/v1/clusters/${clusterId}/nodes/${encodeURIComponent(nodeName)}/reboot`,
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
      apiClient.get<VMResponse[]>(
        `/api/v1/clusters/${clusterId}/vms`,
      ),
    enabled: clusterId.length > 0,
    refetchInterval: 60_000, // WS events handle immediate updates
  });
}
