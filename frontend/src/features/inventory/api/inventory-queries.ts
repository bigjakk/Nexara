import { useQueries } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useMetricStore } from "@/stores/metric-store";
import type {
  ClusterResponse,
  NodeResponse,
  VMResponse,
} from "@/types/api";
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
  liveMetrics: Map<string, { cpuPercent: number; memPercent: number }>,
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
    cpuPercent: live?.cpuPercent ?? null,
    memPercent: live?.memPercent ?? null,
  };
}

function nodeToRow(
  node: NodeResponse,
  cluster: ClusterResponse,
): InventoryRow {
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
    cpuPercent: null,
    memPercent: null,
  };
}

export function useInventoryData() {
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];
  const metricsMap = useMetricStore((s) => s.metrics);

  const nodeQueries = useQueries({
    queries: clusters.map((cluster) => ({
      queryKey: ["clusters", cluster.id, "nodes"],
      queryFn: () =>
        apiClient.get<NodeResponse[]>(
          `/api/v1/clusters/${cluster.id}/nodes`,
        ),
      enabled: clusters.length > 0,
    })),
  });

  const vmQueries = useQueries({
    queries: clusters.map((cluster) => ({
      queryKey: ["clusters", cluster.id, "vms"],
      queryFn: () =>
        apiClient.get<VMResponse[]>(`/api/v1/clusters/${cluster.id}/vms`),
      enabled: clusters.length > 0,
    })),
  });

  const isLoading =
    clustersQuery.isLoading ||
    nodeQueries.some((q) => q.isLoading) ||
    vmQueries.some((q) => q.isLoading);

  const error =
    clustersQuery.error ??
    nodeQueries.find((q) => q.error)?.error ??
    vmQueries.find((q) => q.error)?.error ??
    null;

  let rows: InventoryRow[] = [];

  if (!isLoading && !error && clustersQuery.data) {
    rows = clusters.flatMap((cluster, i) => {
      const nodes = nodeQueries[i]?.data ?? [];
      const vms = vmQueries[i]?.data ?? [];
      const nodeMap = buildNodeMap(nodes);

      // Build live metric lookup from all VM metrics
      const clusterMetrics = metricsMap.get(cluster.id);
      const liveMetrics = clusterMetrics?.vmMetrics ?? new Map<string, { cpuPercent: number; memPercent: number }>();

      const vmRows = vms.map((vm) => vmToRow(vm, cluster, nodeMap, liveMetrics));
      const nodeRows = nodes.map((node) => nodeToRow(node, cluster));
      return [...vmRows, ...nodeRows];
    });
  }

  return { rows, isLoading, error };
}
