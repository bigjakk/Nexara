import type { Node, Edge } from "@xyflow/react";
import type {
  ClusterResponse,
  NodeResponse,
  VMResponse,
  StorageResponse,
} from "@/types/api";

// --- Node data payloads ---

export interface ClusterNodeData {
  kind: "cluster";
  label: string;
  isActive: boolean;
  nodeCount: number;
  vmCount: number;
  clusterId: string;
  [key: string]: unknown;
}

export interface HostNodeData {
  kind: "host";
  label: string;
  status: string;
  cpuCount: number;
  memTotal: number;
  pveVersion: string;
  clusterId: string;
  nodeId: string;
  [key: string]: unknown;
}

export interface GuestNodeData {
  kind: "guest";
  label: string;
  vmid: number;
  type: "qemu" | "lxc";
  status: string;
  cpuCount: number;
  memTotal: number;
  haState: string;
  clusterId: string;
  vmId: string;
  [key: string]: unknown;
}

export interface StorageNodeData {
  kind: "storage";
  label: string;
  storageType: string;
  total: number;
  used: number;
  shared: boolean;
  active: boolean;
  clusterId: string;
  [key: string]: unknown;
}

export type TopologyNodeData =
  | ClusterNodeData
  | HostNodeData
  | GuestNodeData
  | StorageNodeData;

export interface TopologyInput {
  clusters: ClusterResponse[];
  nodesByCluster: Map<string, NodeResponse[]>;
  vmsByCluster: Map<string, VMResponse[]>;
  storageByCluster: Map<string, StorageResponse[]>;
}

export interface TopologyGraph {
  nodes: Node[];
  edges: Edge[];
}

export interface TopologyFilters {
  showVMs: boolean;
  showStorage: boolean;
  selectedClusterId: string | null;
}

/**
 * Transform API data into React Flow nodes + edges.
 * Positions are set to 0,0 here — the layout function handles placement.
 */
export function buildTopologyGraph(
  input: TopologyInput,
  filters: TopologyFilters,
): TopologyGraph {
  const nodes: Node[] = [];
  const edges: Edge[] = [];

  const clustersToShow = filters.selectedClusterId
    ? input.clusters.filter((c) => c.id === filters.selectedClusterId)
    : input.clusters;

  for (const cluster of clustersToShow) {
    const clusterNodes = input.nodesByCluster.get(cluster.id) ?? [];
    const clusterVMs = input.vmsByCluster.get(cluster.id) ?? [];
    const clusterStorage = input.storageByCluster.get(cluster.id) ?? [];

    const clusterNodeId = `cluster-${cluster.id}`;

    // Cluster node
    nodes.push({
      id: clusterNodeId,
      type: "clusterNode",
      position: { x: 0, y: 0 },
      data: {
        kind: "cluster",
        label: cluster.name,
        isActive: cluster.is_active,
        nodeCount: clusterNodes.length,
        vmCount: clusterVMs.length,
        clusterId: cluster.id,
      } satisfies ClusterNodeData,
    });

    // Host nodes
    for (const node of clusterNodes) {
      const hostNodeId = `host-${node.id}`;

      nodes.push({
        id: hostNodeId,
        type: "hostNode",
        position: { x: 0, y: 0 },
        data: {
          kind: "host",
          label: node.name,
          status: node.status,
          cpuCount: node.cpu_count,
          memTotal: node.mem_total,
          pveVersion: node.pve_version,
          clusterId: cluster.id,
          nodeId: node.id,
        } satisfies HostNodeData,
      });

      edges.push({
        id: `edge-${clusterNodeId}-${hostNodeId}`,
        source: clusterNodeId,
        target: hostNodeId,
        type: "smoothstep",
        animated: node.status === "online",
        style: { stroke: getStatusColor(node.status) },
      });
    }

    // VMs / CTs
    if (filters.showVMs) {
      for (const vm of clusterVMs) {
        const guestNodeId = `guest-${vm.id}`;
        const parentHostId = `host-${vm.node_id}`;

        nodes.push({
          id: guestNodeId,
          type: "guestNode",
          position: { x: 0, y: 0 },
          data: {
            kind: "guest",
            label: vm.name || `${vm.type === "qemu" ? "VM" : "CT"} ${String(vm.vmid)}`,
            vmid: vm.vmid,
            type: vm.type as "qemu" | "lxc",
            status: vm.status,
            cpuCount: vm.cpu_count,
            memTotal: vm.mem_total,
            haState: vm.ha_state,
            clusterId: cluster.id,
            vmId: vm.id,
          } satisfies GuestNodeData,
        });

        edges.push({
          id: `edge-${parentHostId}-${guestNodeId}`,
          source: parentHostId,
          target: guestNodeId,
          type: "smoothstep",
          animated: vm.status === "running",
          style: { stroke: getGuestStatusColor(vm.status) },
        });
      }
    }

    // Storage pools
    if (filters.showStorage) {
      // Deduplicate shared storage — only show once per cluster
      const seenSharedStorage = new Set<string>();

      for (const sp of clusterStorage) {
        if (sp.shared) {
          if (seenSharedStorage.has(sp.storage)) continue;
          seenSharedStorage.add(sp.storage);
        }

        const storageNodeId = sp.shared
          ? `storage-shared-${cluster.id}-${sp.storage}`
          : `storage-${sp.id}`;

        nodes.push({
          id: storageNodeId,
          type: "storageNode",
          position: { x: 0, y: 0 },
          data: {
            kind: "storage",
            label: sp.storage,
            storageType: sp.type,
            total: sp.total,
            used: sp.used,
            shared: sp.shared,
            active: sp.active,
            clusterId: cluster.id,
          } satisfies StorageNodeData,
        });

        if (sp.shared) {
          // Connect shared storage to the cluster
          edges.push({
            id: `edge-${clusterNodeId}-${storageNodeId}`,
            source: clusterNodeId,
            target: storageNodeId,
            type: "smoothstep",
            style: { stroke: sp.active ? "#22c55e" : "#6b7280", strokeDasharray: "5,5" },
          });
        } else {
          // Connect local storage to its host node
          const parentHostId = `host-${sp.node_id}`;
          edges.push({
            id: `edge-${parentHostId}-${storageNodeId}`,
            source: parentHostId,
            target: storageNodeId,
            type: "smoothstep",
            style: { stroke: sp.active ? "#22c55e" : "#6b7280", strokeDasharray: "5,5" },
          });
        }
      }
    }
  }

  return { nodes, edges };
}

export function getStatusColor(status: string): string {
  switch (status.toLowerCase()) {
    case "online":
    case "running":
      return "#22c55e"; // green-500
    case "paused":
    case "suspended":
    case "warning":
      return "#eab308"; // yellow-500
    case "offline":
    case "stopped":
    case "error":
    case "critical":
      return "#ef4444"; // red-500
    default:
      return "#6b7280"; // gray-500
  }
}

export function getGuestStatusColor(status: string): string {
  switch (status.toLowerCase()) {
    case "running":
      return "#22c55e";
    case "paused":
    case "suspended":
      return "#eab308";
    case "stopped":
      return "#ef4444";
    default:
      return "#6b7280";
  }
}

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KiB", "MiB", "GiB", "TiB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  const value = bytes / Math.pow(k, i);
  return `${value.toFixed(1)} ${sizes[i] ?? "B"}`;
}
