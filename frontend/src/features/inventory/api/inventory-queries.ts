import { useQueries } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useDashboardMetrics } from "@/hooks/useMetrics";
import type {
  ClusterResponse,
  NodeResponse,
  VMResponse,
} from "@/types/api";
import type { AggregatedMetrics, VmLiveMetric } from "@/types/ws";
import type { InventoryRow, ResourceStatus, ResourceType } from "../types/inventory";

function normalizeStatus(raw: string): ResourceStatus {
  const map: Record<string, ResourceStatus> = {
    running: "running",
    stopped: "stopped",
    paused: "paused",
    suspended: "suspended",
    online: "online",
    offline: "offline",
  };
  return map[raw.toLowerCase()] ?? "unknown";
}

function vmTypeToResourceType(type: string): ResourceType {
  return type === "lxc" ? "ct" : "vm";
}

function buildNodeMap(nodes: NodeResponse[]): Map<string, string> {
  const map = new Map<string, string>();
  for (const node of nodes) {
    map.set(node.id, node.name);
  }
  return map;
}

function vmToRow(
  vm: VMResponse,
  cluster: ClusterResponse,
  nodeMap: Map<string, string>,
  liveMetrics: Map<string, VmLiveMetric>,
): InventoryRow {
  const type = vmTypeToResourceType(vm.type);
  const live = liveMetrics.get(vm.id);
  return {
    key: `${cluster.id}:${type}:${vm.id}`,
    id: vm.id,
    type,
    name: vm.name,
    status: normalizeStatus(vm.status),
    clusterName: cluster.name,
    clusterId: cluster.id,
    nodeName: nodeMap.get(vm.node_id) ?? "",
    vmid: vm.vmid,
    cpuCount: vm.cpu_count,
    memTotal: vm.mem_total,
    diskTotal: vm.disk_total,
    uptime: vm.uptime,
    tags: vm.tags,
    haState: vm.ha_state,
    pool: vm.pool,
    template: vm.template,
    ostype: vm.ostype,
    configOstype: vm.config_ostype,
    cpuPercent: live?.cpuPercent ?? null,
    memPercent: live?.memPercent ?? null,
    diskReadBps: live?.diskReadBps ?? null,
    diskWriteBps: live?.diskWriteBps ?? null,
    netInBps: live?.netInBps ?? null,
    netOutBps: live?.netOutBps ?? null,
  };
}

function nodeToRow(
  node: NodeResponse,
  cluster: ClusterResponse,
  liveMetrics: Map<string, VmLiveMetric>,
): InventoryRow {
  const live = liveMetrics.get(node.id);
  return {
    key: `${cluster.id}:node:${node.id}`,
    id: node.id,
    type: "node",
    name: node.name,
    status: normalizeStatus(node.status),
    clusterName: cluster.name,
    clusterId: cluster.id,
    nodeName: node.name,
    vmid: null,
    cpuCount: node.cpu_count,
    memTotal: node.mem_total,
    diskTotal: node.disk_total,
    uptime: node.uptime,
    tags: "",
    haState: "",
    pool: "",
    template: false,
    ostype: "",
    configOstype: "",
    cpuPercent: live?.cpuPercent ?? null,
    memPercent: live?.memPercent ?? null,
    diskReadBps: live?.diskReadBps ?? null,
    diskWriteBps: live?.diskWriteBps ?? null,
    netInBps: live?.netInBps ?? null,
    netOutBps: live?.netOutBps ?? null,
  };
}

export interface ClusterInventoryEntry {
  cluster: ClusterResponse;
  nodes: NodeResponse[] | undefined;
  vms: VMResponse[] | undefined;
  /** True when a nodes/vms query for this cluster is in error state. */
  errored: boolean;
}

/** Build display rows from per-cluster results, isolating failures: a cluster
 * with no data is skipped (and reported in failedClusterIds when its queries
 * errored) instead of blanking every other cluster's rows. A cluster whose
 * refetch failed but still has cached data keeps rendering that stale data —
 * TanStack keeps `data` alongside `error` in that case, and it beats showing
 * nothing. */
export function buildInventoryRows(
  entries: ClusterInventoryEntry[],
  metricsMap: Map<string, AggregatedMetrics>,
): { rows: InventoryRow[]; failedClusterIds: string[] } {
  const rows: InventoryRow[] = [];
  const failedClusterIds: string[] = [];

  for (const { cluster, nodes, vms, errored } of entries) {
    if (!nodes || !vms) {
      if (errored) failedClusterIds.push(cluster.id);
      continue;
    }
    const nodeMap = buildNodeMap(nodes);
    const clusterMetrics = metricsMap.get(cluster.id);
    const vmLive = clusterMetrics?.vmMetrics ?? new Map<string, VmLiveMetric>();
    const nodeLive = clusterMetrics?.nodeMetrics ?? new Map<string, VmLiveMetric>();

    for (const vm of vms) rows.push(vmToRow(vm, cluster, nodeMap, vmLive));
    for (const node of nodes) rows.push(nodeToRow(node, cluster, nodeLive));
  }

  return { rows, failedClusterIds };
}

export function useInventoryData() {
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];
  const clusterIds = clusters.map((c) => c.id);

  // Subscribe to live WebSocket metrics for all clusters
  const metricsMap = useDashboardMetrics(clusterIds);

  const nodeQueries = useQueries({
    queries: clusters.map((cluster) => ({
      queryKey: ["clusters", cluster.id, "nodes"],
      queryFn: () =>
        apiClient.list<NodeResponse>(
          `/api/v1/clusters/${cluster.id}/nodes`,
        ),
      enabled: clusters.length > 0,
    })),
  });

  const vmQueries = useQueries({
    queries: clusters.map((cluster) => ({
      queryKey: ["clusters", cluster.id, "vms"],
      queryFn: () =>
        apiClient.list<VMResponse>(`/api/v1/clusters/${cluster.id}/vms`),
      enabled: clusters.length > 0,
    })),
  });

  const isLoading =
    clustersQuery.isLoading ||
    nodeQueries.some((q) => q.isLoading) ||
    vmQueries.some((q) => q.isLoading);

  // Only a failure of the cluster list itself is fatal — per-cluster failures
  // degrade to failedClusterIds so one unreachable cluster doesn't hide the
  // healthy ones' guests.
  const error = clustersQuery.error ?? null;

  const { rows, failedClusterIds } = buildInventoryRows(
    clusters.map((cluster, i) => ({
      cluster,
      nodes: nodeQueries[i]?.data,
      vms: vmQueries[i]?.data,
      errored: Boolean(nodeQueries[i]?.error ?? vmQueries[i]?.error),
    })),
    metricsMap,
  );

  return { rows, isLoading, error, failedClusterIds };
}
