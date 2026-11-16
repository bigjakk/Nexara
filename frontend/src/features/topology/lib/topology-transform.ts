import type { Node, Edge } from "@xyflow/react";
import type {
  ClusterResponse,
  NodeResponse,
  VMResponse,
  StorageResponse,
} from "@/types/api";
import { guestState } from "@/lib/guest-state";
import type { UpsTone } from "@/features/power/lib/ups-state";

// --- Node data payloads ---

export interface ClusterNodeData {
  kind: "cluster";
  label: string;
  isActive: boolean;
  status: string;
  nodeCount: number;
  vmCount: number;
  pveVersion: string;
  clusterId: string;
  [key: string]: unknown;
}

/** Compact per-guest entry rendered as a cell inside its host card. */
export interface HostGuest {
  vmId: string;
  vmid: number;
  name: string;
  type: "qemu" | "lxc";
  status: string;
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
  guests: HostGuest[];
  /** Injected live metrics (0-100); undefined until the first WS message. */
  cpuPercent?: number;
  memPercent?: number;
  /** Injected with them: each running guest's CPU (0-100) by its id. */
  guestCpu?: Record<string, number>;
  /** Injected by the power overlay: the UPSes that power it with no upsmon on it. */
  noUpsmon?: string[];
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
  /** DB row id used by the storage detail route. For shared pools (one row
   * per node) this is the first-seen row — the detail page resolves any of
   * a shared pool's row ids within the cluster. */
  storageId: string;
  [key: string]: unknown;
}

/** A UPS on the power overlay, which topology-power.ts places outside the layout. */
export interface UpsNodeData {
  kind: "ups";
  label: string;
  deviceId: string;
  serverName: string;
  status: string;
  tone: UpsTone;
  batteryCharge: number | null;
  batteryRuntime: number | null;
  direction: "TB" | "LR";
  [key: string]: unknown;
}

export type TopologyNodeData =
  | ClusterNodeData
  | HostNodeData
  | GuestNodeData
  | StorageNodeData
  | UpsNodeData;

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

    // A template is not a guest anyone runs: not counted, and no cell.
    const clusterGuests = clusterVMs.filter((vm) => !vm.template);

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
        status: cluster.status,
        nodeCount: clusterNodes.length,
        vmCount: clusterGuests.length,
        pveVersion: cluster.pve_version,
        clusterId: cluster.id,
      } satisfies ClusterNodeData,
    });

    // Host nodes
    for (const node of clusterNodes) {
      const hostNodeId = `host-${node.id}`;

      const hostGuests: HostGuest[] = clusterGuests
        .filter((vm) => vm.node_id === node.id)
        .sort((a, b) => a.vmid - b.vmid)
        .map((vm) => ({
          vmId: vm.id,
          vmid: vm.vmid,
          name: vm.name,
          type: vm.type as "qemu" | "lxc",
          status: vm.status,
        }));

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
          guests: hostGuests,
        } satisfies HostNodeData,
      });

      edges.push({
        id: `edge-${clusterNodeId}-${hostNodeId}`,
        source: clusterNodeId,
        target: hostNodeId,
        type: "smoothstep",
        style: treeEdgeStyle(node.status === "offline" ? "err" : "ok"),
      });
    }

    // VMs / CTs
    if (filters.showVMs) {
      for (const vm of clusterGuests) {
        const guestNodeId = `guest-${vm.id}`;
        const parentHostId = `host-${vm.node_id}`;

        nodes.push({
          id: guestNodeId,
          type: "guestNode",
          position: { x: 0, y: 0 },
          data: {
            kind: "guest",
            label:
              vm.name ||
              `${vm.type === "qemu" ? "VM" : "CT"} ${String(vm.vmid)}`,
            vmid: vm.vmid,
            type: vm.type as "qemu" | "lxc",
            // A halted VM is running to Proxmox; its state says what it is.
            status: guestState(vm.status, vm.run_state),
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
          style: treeEdgeStyle("ok"),
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
            storageId: sp.id,
          } satisfies StorageNodeData,
        });

        if (sp.shared) {
          // Connect shared storage to the cluster
          edges.push({
            id: `edge-${clusterNodeId}-${storageNodeId}`,
            source: clusterNodeId,
            target: storageNodeId,
            type: "smoothstep",
            style: treeEdgeStyle(sp.active ? "ok" : "warn"),
          });
        } else {
          // Connect local storage to its host node
          const parentHostId = `host-${sp.node_id}`;
          edges.push({
            id: `edge-${parentHostId}-${storageNodeId}`,
            source: parentHostId,
            target: storageNodeId,
            type: "smoothstep",
            style: treeEdgeStyle(sp.active ? "ok" : "warn"),
          });
        }
      }
    }
  }

  return { nodes, edges };
}

/** The stroke every edge that has nothing to report is drawn in. */
export const EDGE_NEUTRAL = "hsl(var(--foreground) / 0.3)";

/**
 * A tree edge is neutral and still. Only an edge to something in trouble (an
 * offline host, an inactive store) takes that state's colour, dashed.
 */
export function treeEdgeStyle(tone: "ok" | "warn" | "err"): {
  stroke: string;
  strokeWidth: number;
  strokeDasharray?: string;
} {
  if (tone === "ok") return { stroke: EDGE_NEUTRAL, strokeWidth: 1.25 };
  return {
    stroke: tone === "err" ? "#ef4444" : "#f59e0b",
    strokeWidth: 1.25,
    strokeDasharray: "5 5",
  };
}

// Re-exported from the canonical lib for the topology nodes that import
// it from this file, so topology follows the app-wide byte-unit preference.
export { formatBytes } from "@/lib/format";
