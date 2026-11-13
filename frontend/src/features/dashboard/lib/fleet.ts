/**
 * The Fleet widget's view model: every cluster, its nodes with their live
 * load, and each node's guests. Pure, so the shading and counting rules are
 * tested without rendering anything.
 */
import type { NodeResponse, VMResponse } from "@/types/api";
import type { AggregatedMetrics } from "@/types/ws";
import type { ClusterSummary } from "../api/dashboard-queries";
import type { InventoryRow } from "@/features/inventory/types/inventory";

/** How strongly a running guest's cell is shaded, idle to busy. */
export type FleetLoad = 1 | 2 | 3 | 4;

export interface FleetGuest {
  id: string;
  vmid: number;
  name: string;
  kind: "vm" | "ct";
  /** The raw status, for the label of a guest that is not running. */
  status: string;
  running: boolean;
  /** CPU of a running guest, 0-100. Null until its first metric arrives. */
  cpuPercent: number | null;
  /** Null for a guest that is not running: it has no load to shade. */
  load: FleetLoad | null;
  href: string;
}

export interface FleetNode {
  id: string;
  name: string;
  online: boolean;
  status: string;
  /** Null until the node's first metric arrives, which is not 0%. */
  cpuPercent: number | null;
  memPercent: number | null;
  memUsedBytes: number | null;
  memTotalBytes: number;
  guests: FleetGuest[];
  href: string;
}

export interface FleetCluster {
  id: string;
  name: string;
  version: string;
  nodeCount: number;
  nodesOnline: number;
  vmCount: number;
  containerCount: number;
  /** Null when the cluster reports no storage at all. */
  storagePercent: number | null;
  storageTotalBytes: number;
  nodes: FleetNode[];
  href: string;
}

export interface Fleet {
  clusters: FleetCluster[];
  nodeCount: number;
  guestCount: number;
  guestsRunning: number;
}

/** A guest with no reading yet is drawn at the idle shade, not left blank. */
export function loadLevel(cpuPercent: number | null): FleetLoad {
  if (cpuPercent === null || cpuPercent < 5) return 1;
  if (cpuPercent < 25) return 2;
  if (cpuPercent < 60) return 3;
  return 4;
}

/**
 * A guest cell from an inventory row, for pages that already hold the
 * inventory instead of the dashboard's lists. Null for a template or a node.
 */
export function fleetGuestFromRow(row: InventoryRow): FleetGuest | null {
  if (row.type === "node" || row.template) return null;
  const running = row.status === "running";
  const cpuPercent = running ? row.cpuPercent : null;
  return {
    id: row.id,
    vmid: row.vmid ?? 0,
    name: row.name,
    kind: row.type,
    status: row.status,
    running,
    cpuPercent,
    load: running ? loadLevel(cpuPercent) : null,
    href: `/inventory/${row.type}/${row.clusterId}/${row.id}`,
  };
}

export function buildFleet(
  summaries: ClusterSummary[],
  nodesByCluster: ReadonlyMap<string, NodeResponse[]>,
  vmsByCluster: ReadonlyMap<string, VMResponse[]>,
  metrics: ReadonlyMap<string, AggregatedMetrics>,
): Fleet {
  let nodeCount = 0;
  let guestCount = 0;
  let guestsRunning = 0;

  const clusters = summaries.map((summary): FleetCluster => {
    const { cluster } = summary;
    const live = metrics.get(cluster.id);
    // A template is not a guest anyone runs: no cell, and not counted.
    const guests = (vmsByCluster.get(cluster.id) ?? []).filter(
      (vm) => !vm.template,
    );

    const nodes = [...(nodesByCluster.get(cluster.id) ?? [])]
      .sort((a, b) =>
        a.name.localeCompare(b.name, undefined, { numeric: true }),
      )
      .map((node): FleetNode => {
        const nodeMetric = live?.nodeMetrics.get(node.id);
        const memPercent = nodeMetric?.memPercent ?? null;
        return {
          id: node.id,
          name: node.name,
          online: node.status === "online",
          status: node.status,
          cpuPercent: nodeMetric?.cpuPercent ?? null,
          memPercent,
          memUsedBytes:
            memPercent === null ? null : (memPercent / 100) * node.mem_total,
          memTotalBytes: node.mem_total,
          guests: guests
            .filter((vm) => vm.node_id === node.id)
            .sort((a, b) => a.vmid - b.vmid)
            .map((vm): FleetGuest => {
              const kind = vm.type === "lxc" ? "ct" : "vm";
              const running = vm.status === "running";
              const cpuPercent = running
                ? (live?.vmMetrics.get(vm.id)?.cpuPercent ?? null)
                : null;
              return {
                id: vm.id,
                vmid: vm.vmid,
                name: vm.name,
                kind,
                status: vm.status,
                running,
                cpuPercent,
                load: running ? loadLevel(cpuPercent) : null,
                href: `/inventory/${kind}/${cluster.id}/${vm.id}`,
              };
            }),
          href: `/clusters/${cluster.id}/nodes/${node.id}`,
        };
      });

    nodeCount += nodes.length;
    guestCount += guests.length;
    guestsRunning += guests.filter((vm) => vm.status === "running").length;

    return {
      id: cluster.id,
      name: cluster.name,
      version: cluster.pve_version,
      nodeCount: nodes.length,
      nodesOnline: nodes.filter((n) => n.online).length,
      vmCount: guests.filter((vm) => vm.type !== "lxc").length,
      containerCount: guests.filter((vm) => vm.type === "lxc").length,
      storagePercent:
        summary.storageTotalBytes > 0
          ? (summary.storageUsedBytes / summary.storageTotalBytes) * 100
          : null,
      storageTotalBytes: summary.storageTotalBytes,
      nodes,
      href: `/clusters/${cluster.id}`,
    };
  });

  return { clusters, nodeCount, guestCount, guestsRunning };
}
