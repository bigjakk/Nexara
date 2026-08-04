import { describe, expect, it } from "vitest";
import { buildInventoryRows } from "./inventory-queries";
import type {
  ClusterResponse,
  NodeResponse,
  VMResponse,
} from "@/types/api";
import type { AggregatedMetrics, VmLiveMetric } from "@/types/ws";

function cluster(id: string, name: string): ClusterResponse {
  return {
    id,
    name,
    api_url: `https://${name}:8006`,
    token_id: "t",
    tls_fingerprint: "",
    sync_interval_seconds: 60,
    is_active: true,
    status: "online",
    pve_version: "9.2",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function node(id: string, clusterId: string, name: string): NodeResponse {
  return {
    id,
    cluster_id: clusterId,
    name,
    address: "10.0.0.1",
    status: "online",
    ha_state: "",
    cpu_count: 8,
    cpu_model: "x",
    cpu_cores: 4,
    cpu_sockets: 1,
    cpu_threads: 2,
    cpu_mhz: "2900",
    mem_total: 1024,
    disk_total: 2048,
    swap_total: 0,
    swap_used: 0,
    swap_free: 0,
    pve_version: "9.2",
    kernel_version: "6.8",
    dns_servers: "",
    dns_search: "",
    timezone: "UTC",
    subscription_status: "",
    subscription_level: "",
    load_avg: "",
    io_wait: 0,
    uptime: 100,
    last_seen_at: "2026-01-01T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function vm(
  id: string,
  clusterId: string,
  nodeId: string,
  vmid: number,
): VMResponse {
  return {
    id,
    cluster_id: clusterId,
    node_id: nodeId,
    vmid,
    name: `vm-${String(vmid)}`,
    type: "qemu",
    status: "running",
    cpu_count: 2,
    mem_total: 512,
    disk_total: 1024,
    uptime: 50,
    template: false,
    tags: "",
    ha_state: "",
    pool: "",
    ostype: "",
    config_ostype: "",
    last_seen_at: "2026-01-01T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function aggregated(vmMetrics: Map<string, VmLiveMetric>): AggregatedMetrics {
  return {
    cpuPercent: 0,
    memPercent: 0,
    memUsed: 0,
    memTotal: 0,
    diskReadBps: 0,
    diskWriteBps: 0,
    netInBps: 0,
    netOutBps: 0,
    nodeCount: 0,
    vmCount: 0,
    history: [],
    topConsumers: [],
    vmMetrics,
    nodeMetrics: new Map<string, VmLiveMetric>(),
  };
}

const emptyMetrics = new Map<string, AggregatedMetrics>();

describe("buildInventoryRows", () => {
  it("keeps healthy clusters' rows when another cluster errors", () => {
    const good = cluster("c1", "good");
    const bad = cluster("c2", "bad");
    const { rows, failedClusterIds } = buildInventoryRows(
      [
        {
          cluster: good,
          nodes: [node("n1", "c1", "hv01")],
          vms: [vm("v1", "c1", "n1", 100)],
          errored: false,
        },
        { cluster: bad, nodes: undefined, vms: undefined, errored: true },
      ],
      emptyMetrics,
    );

    expect(failedClusterIds).toEqual(["c2"]);
    expect(rows.map((r) => r.key)).toEqual(["c1:vm:v1", "c1:node:n1"]);
    expect(rows[0]?.nodeName).toBe("hv01");
  });

  it("still renders a cluster whose refetch errored but has cached data", () => {
    const c = cluster("c1", "flaky");
    const { rows, failedClusterIds } = buildInventoryRows(
      [
        {
          cluster: c,
          nodes: [node("n1", "c1", "hv01")],
          vms: [vm("v1", "c1", "n1", 100)],
          errored: true,
        },
      ],
      emptyMetrics,
    );

    expect(failedClusterIds).toEqual([]);
    expect(rows).toHaveLength(2);
  });

  it("skips a still-loading cluster without marking it failed", () => {
    const c = cluster("c1", "loading");
    const { rows, failedClusterIds } = buildInventoryRows(
      [{ cluster: c, nodes: undefined, vms: undefined, errored: false }],
      emptyMetrics,
    );

    expect(rows).toEqual([]);
    expect(failedClusterIds).toEqual([]);
  });

  it("merges live metrics into rows", () => {
    const c = cluster("c1", "good");
    const live: VmLiveMetric = {
      cpuPercent: 42,
      memPercent: 60,
      diskReadBps: 1,
      diskWriteBps: 2,
      netInBps: 3,
      netOutBps: 4,
    };
    const metrics = new Map<string, AggregatedMetrics>([
      ["c1", aggregated(new Map([["v1", live]]))],
    ]);

    const { rows } = buildInventoryRows(
      [
        {
          cluster: c,
          nodes: [node("n1", "c1", "hv01")],
          vms: [vm("v1", "c1", "n1", 100)],
          errored: false,
        },
      ],
      metrics,
    );

    expect(rows[0]?.cpuPercent).toBe(42);
    expect(rows[0]?.memPercent).toBe(60);
  });
});
