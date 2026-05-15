import { describe, it, expect, beforeEach, vi, afterEach } from "vitest";
import { useMetricStore, MAX_HISTORY_POINTS } from "./metric-store";
import type {
  AggregatedMetrics,
  ClusterMetricSummary,
  NodeMetricSnapshot,
  VmMetricSnapshot,
} from "@/types/ws";

function makeNode(overrides: Partial<NodeMetricSnapshot>): NodeMetricSnapshot {
  return {
    node_id: "node-x",
    cpu_usage: 0,
    mem_used: 0,
    mem_total: 0,
    disk_read: 0,
    disk_write: 0,
    net_in: 0,
    net_out: 0,
    disk_read_bps: 0,
    disk_write_bps: 0,
    net_in_bps: 0,
    net_out_bps: 0,
    ...overrides,
  };
}

function makeVM(overrides: Partial<VmMetricSnapshot>): VmMetricSnapshot {
  return {
    vm_id: "vm-x",
    cpu_usage: 0,
    mem_used: 0,
    mem_total: 0,
    disk_read: 0,
    disk_write: 0,
    net_in: 0,
    net_out: 0,
    disk_read_bps: 0,
    disk_write_bps: 0,
    net_in_bps: 0,
    net_out_bps: 0,
    ...overrides,
  };
}

function createPayload(overrides?: Partial<ClusterMetricSummary>): ClusterMetricSummary {
  return {
    cluster_id: "test-cluster",
    collected_at: new Date().toISOString(),
    node_count: 2,
    vm_count: 3,
    nodes: [
      makeNode({
        node_id: "node-1",
        cpu_usage: 0.5,
        mem_used: 4_000_000_000,
        mem_total: 8_000_000_000,
        disk_read: 100_000,
        disk_write: 50_000,
        net_in: 200_000,
        net_out: 100_000,
      }),
      makeNode({
        node_id: "node-2",
        cpu_usage: 0.3,
        mem_used: 2_000_000_000,
        mem_total: 8_000_000_000,
        disk_read: 80_000,
        disk_write: 40_000,
        net_in: 150_000,
        net_out: 80_000,
      }),
    ],
    vms: [
      makeVM({
        vm_id: "vm-1",
        cpu_usage: 0.8,
        mem_used: 2_000_000_000,
        mem_total: 4_000_000_000,
        disk_read: 50_000,
        disk_write: 25_000,
        net_in: 100_000,
        net_out: 50_000,
      }),
      makeVM({
        vm_id: "vm-2",
        cpu_usage: 0.2,
        mem_used: 1_000_000_000,
        mem_total: 2_000_000_000,
        disk_read: 30_000,
        disk_write: 15_000,
        net_in: 80_000,
        net_out: 40_000,
      }),
      makeVM({
        vm_id: "vm-3",
        cpu_usage: 0.6,
        mem_used: 3_000_000_000,
        mem_total: 4_000_000_000,
        disk_read: 40_000,
        disk_write: 20_000,
        net_in: 90_000,
        net_out: 45_000,
      }),
    ],
    ...overrides,
  };
}

function getMetrics(clusterId: string): AggregatedMetrics {
  const m = useMetricStore.getState().metrics.get(clusterId);
  if (!m) throw new Error(`No metrics for cluster ${clusterId}`);
  return m;
}

describe("metric-store", () => {
  beforeEach(() => {
    useMetricStore.getState().clearAll();
    // Set minimum refresh interval so rapid calls are not throttled
    useMetricStore.getState().setRefreshInterval(0);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("processes a metric message and stores aggregated data", () => {
    const payload = createPayload();
    useMetricStore.getState().processMetricMessage("test-cluster", payload);

    const metrics = getMetrics("test-cluster");
    expect(metrics.nodeCount).toBe(2);
    expect(metrics.vmCount).toBe(3);
    // Average CPU: (0.5 + 0.3) / 2 * 100 = 40%
    expect(metrics.cpuPercent).toBe(40);
    // Memory: (4B + 2B) / (8B + 8B) * 100 = 37.5%
    expect(metrics.memPercent).toBe(37.5);
  });

  it("sums per-node rates into the aggregated rate fields", () => {
    const payload = createPayload({
      nodes: [
        makeNode({ node_id: "node-1", disk_read_bps: 1000, net_in_bps: 500 }),
        makeNode({ node_id: "node-2", disk_read_bps: 2500, net_in_bps: 750 }),
      ],
    });
    useMetricStore.getState().processMetricMessage("test-cluster", payload);

    const metrics = getMetrics("test-cluster");
    expect(metrics.diskReadBps).toBe(3500);
    expect(metrics.netInBps).toBe(1250);
  });

  it("propagates per-VM rates from the payload to vmMetrics", () => {
    const payload = createPayload({
      vms: [
        makeVM({
          vm_id: "vm-1",
          cpu_usage: 0.5,
          mem_total: 1_000_000_000,
          mem_used: 500_000_000,
          disk_read_bps: 12345,
          net_out_bps: 6789,
        }),
      ],
      vm_count: 1,
    });
    useMetricStore.getState().processMetricMessage("test-cluster", payload);

    const m = getMetrics("test-cluster").vmMetrics.get("vm-1");
    expect(m?.diskReadBps).toBe(12345);
    expect(m?.netOutBps).toBe(6789);
  });

  it("appends to history on each message", () => {
    const payload = createPayload();
    useMetricStore.getState().processMetricMessage("test-cluster", payload);
    useMetricStore.getState().processMetricMessage("test-cluster", payload);
    useMetricStore.getState().processMetricMessage("test-cluster", payload);

    const metrics = getMetrics("test-cluster");
    expect(metrics.history).toHaveLength(3);
  });

  it("caps rolling buffer at MAX_HISTORY_POINTS", () => {
    const payload = createPayload();
    for (let i = 0; i < MAX_HISTORY_POINTS + 10; i++) {
      useMetricStore.getState().processMetricMessage("test-cluster", payload);
    }

    const metrics = getMetrics("test-cluster");
    expect(metrics.history).toHaveLength(MAX_HISTORY_POINTS);
  });

  it("sorts top consumers by CPU descending", () => {
    const payload = createPayload();
    useMetricStore.getState().processMetricMessage("test-cluster", payload);

    const metrics = getMetrics("test-cluster");
    const consumers = metrics.topConsumers;
    expect(consumers).toHaveLength(3);
    // vm-1 has highest CPU (0.8*100=80%), then vm-3 (60%), then vm-2 (20%)
    expect(consumers[0]?.vmId).toBe("vm-1");
    expect(consumers[1]?.vmId).toBe("vm-3");
    expect(consumers[2]?.vmId).toBe("vm-2");
  });

  it("limits top consumers to 10", () => {
    const vms = Array.from({ length: 15 }, (_, i) =>
      makeVM({
        vm_id: `vm-${String(i)}`,
        cpu_usage: Math.random(),
        mem_used: 1_000_000_000,
        mem_total: 2_000_000_000,
      }),
    );
    const payload = createPayload({ vms, vm_count: 15 });
    useMetricStore.getState().processMetricMessage("test-cluster", payload);

    const metrics = getMetrics("test-cluster");
    expect(metrics.topConsumers).toHaveLength(10);
  });

  it("vmMetrics contains ALL VMs, not just top 10", () => {
    const vms = Array.from({ length: 15 }, (_, i) =>
      makeVM({
        vm_id: `vm-${String(i)}`,
        cpu_usage: 0.5,
        mem_used: 1_000_000_000,
        mem_total: 2_000_000_000,
      }),
    );
    const payload = createPayload({ vms, vm_count: 15 });
    useMetricStore.getState().processMetricMessage("test-cluster", payload);

    const metrics = getMetrics("test-cluster");
    expect(metrics.vmMetrics.size).toBe(15);
    const vm0 = metrics.vmMetrics.get("vm-0");
    expect(vm0).toBeDefined();
    expect(vm0?.cpuPercent).toBe(50);
    expect(vm0?.memPercent).toBe(50);
  });

  it("clearCluster removes specific cluster data", () => {
    const payload = createPayload();
    useMetricStore.getState().processMetricMessage("test-cluster", payload);
    useMetricStore.getState().processMetricMessage("other-cluster", payload);

    useMetricStore.getState().clearCluster("test-cluster");

    expect(useMetricStore.getState().metrics.has("test-cluster")).toBe(false);
    expect(useMetricStore.getState().metrics.has("other-cluster")).toBe(true);
  });

  it("clearAll removes all data", () => {
    const payload = createPayload();
    useMetricStore.getState().processMetricMessage("test-cluster", payload);
    useMetricStore.getState().processMetricMessage("other-cluster", payload);

    useMetricStore.getState().clearAll();

    expect(useMetricStore.getState().metrics.size).toBe(0);
  });
});
