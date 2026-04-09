/**
 * Tests for the live metric store. Pure logic — no React, no native
 * deps. The store is a Zustand factory; we can call its actions directly
 * and inspect the resulting state via getState().
 *
 * Coverage targets:
 *   - Aggregation: cluster CPU% / Mem% / counts derived from per-node data
 *   - Per-VM and per-node Maps populated correctly
 *   - Rate calculation: BPS computed from cumulative counter diffs
 *   - Dedupe: same `collected_at` processed twice doesn't zero the rates
 *     (the race condition fix from the live metrics work)
 *   - Lifecycle: clearCluster + clearAll wipe the right keys
 *   - First-message edge case: no prev snapshot → BPS rates are zero, not NaN
 */

import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  jest,
} from "@jest/globals";

import type { ClusterMetricSummary } from "@/features/api/metric-ws-types";

import { useMetricStore } from "./metric-store";

// Wipe the store between tests so each test starts from a clean slate.
beforeEach(() => {
  useMetricStore.getState().clearAll();
});

const CLUSTER = "11111111-1111-1111-1111-111111111111";
const NODE_A = "22222222-2222-2222-2222-222222222222";
const NODE_B = "33333333-3333-3333-3333-333333333333";
const VM_1 = "44444444-4444-4444-4444-444444444444";
const VM_2 = "55555555-5555-5555-5555-555555555555";

function makePayload(opts: {
  collectedAt: string;
  cpuA?: number;
  cpuB?: number;
  memUsedA?: number;
  memTotalA?: number;
  memUsedB?: number;
  memTotalB?: number;
  diskReadA?: number;
  netInA?: number;
  netOutA?: number;
  diskReadB?: number;
  vm1Cpu?: number;
  vm2Cpu?: number;
}): ClusterMetricSummary {
  return {
    cluster_id: CLUSTER,
    collected_at: opts.collectedAt,
    node_count: 2,
    vm_count: 2,
    nodes: [
      {
        node_id: NODE_A,
        cpu_usage: opts.cpuA ?? 0.5,
        mem_used: opts.memUsedA ?? 4 * 1024 * 1024 * 1024,
        mem_total: opts.memTotalA ?? 16 * 1024 * 1024 * 1024,
        disk_read: opts.diskReadA ?? 0,
        disk_write: 0,
        net_in: opts.netInA ?? 0,
        net_out: opts.netOutA ?? 0,
      },
      {
        node_id: NODE_B,
        cpu_usage: opts.cpuB ?? 0.25,
        mem_used: opts.memUsedB ?? 8 * 1024 * 1024 * 1024,
        mem_total: opts.memTotalB ?? 32 * 1024 * 1024 * 1024,
        disk_read: opts.diskReadB ?? 0,
        disk_write: 0,
        net_in: 0,
        net_out: 0,
      },
    ],
    vms: [
      {
        vm_id: VM_1,
        cpu_usage: opts.vm1Cpu ?? 0.8,
        mem_used: 1 * 1024 * 1024 * 1024,
        mem_total: 4 * 1024 * 1024 * 1024,
        disk_read: 0,
        disk_write: 0,
        net_in: 0,
        net_out: 0,
      },
      {
        vm_id: VM_2,
        cpu_usage: opts.vm2Cpu ?? 0.1,
        mem_used: 512 * 1024 * 1024,
        mem_total: 2 * 1024 * 1024 * 1024,
        disk_read: 0,
        disk_write: 0,
        net_in: 0,
        net_out: 0,
      },
    ],
  };
}

describe("metric-store: aggregation", () => {
  it("averages CPU across nodes and computes memory percent from totals", () => {
    const store = useMetricStore.getState();
    store.processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:00Z",
        cpuA: 0.6,
        cpuB: 0.4,
      }),
    );

    const aggregated = useMetricStore.getState().clusters.get(CLUSTER);
    expect(aggregated).toBeDefined();
    // (0.6 + 0.4) / 2 nodes = 0.5 → 50%
    expect(aggregated?.cpuPercent).toBeCloseTo(50, 4);
    // (4 + 8) / (16 + 32) = 12 / 48 = 25%
    expect(aggregated?.memPercent).toBeCloseTo(25, 4);
    expect(aggregated?.nodeCount).toBe(2);
    expect(aggregated?.vmCount).toBe(2);
  });

  it("populates per-node and per-VM maps with percent values", () => {
    const store = useMetricStore.getState();
    store.processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:00Z",
        cpuA: 0.75,
        vm1Cpu: 0.9,
      }),
    );

    const state = useMetricStore.getState();
    expect(state.nodes.get(NODE_A)?.cpuPercent).toBeCloseTo(75, 4);
    expect(state.nodes.get(NODE_A)?.memPercent).toBeCloseTo(25, 4);
    expect(state.vms.get(VM_1)?.cpuPercent).toBeCloseTo(90, 4);
    // VM_1 mem: 1 GiB / 4 GiB = 25%
    expect(state.vms.get(VM_1)?.memPercent).toBeCloseTo(25, 4);
  });

  it("handles zero mem_total without dividing by zero", () => {
    const payload = makePayload({
      collectedAt: "2026-04-08T12:00:00Z",
      memUsedA: 0,
      memTotalA: 0,
      memUsedB: 0,
      memTotalB: 0,
    });
    useMetricStore.getState().processMessage(CLUSTER, payload);
    const aggregated = useMetricStore.getState().clusters.get(CLUSTER);
    expect(aggregated?.memPercent).toBe(0);
    // Per-node maps should also handle the zero case
    expect(useMetricStore.getState().nodes.get(NODE_A)?.memPercent).toBe(0);
  });

  it("handles zero node count gracefully", () => {
    const payload: ClusterMetricSummary = {
      cluster_id: CLUSTER,
      collected_at: "2026-04-08T12:00:00Z",
      node_count: 0,
      vm_count: 0,
      nodes: [],
      vms: [],
    };
    useMetricStore.getState().processMessage(CLUSTER, payload);
    const aggregated = useMetricStore.getState().clusters.get(CLUSTER);
    expect(aggregated?.cpuPercent).toBe(0);
    expect(aggregated?.memPercent).toBe(0);
    expect(aggregated?.nodeCount).toBe(0);
  });
});

describe("metric-store: rate calculation", () => {
  // Pin Date.now so the dt calculations are deterministic.
  let now = 1_700_000_000_000;
  beforeEach(() => {
    now = 1_700_000_000_000;
    jest.spyOn(Date, "now").mockImplementation(() => now);
  });
  afterEach(() => {
    jest.restoreAllMocks();
  });

  it("returns zero BPS on the first message (no prev snapshot)", () => {
    useMetricStore.getState().processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:00Z",
        diskReadA: 1_000_000,
        netInA: 500_000,
      }),
    );
    const a = useMetricStore.getState().clusters.get(CLUSTER);
    expect(a?.diskReadBps).toBe(0);
    expect(a?.netInBps).toBe(0);
    expect(a?.diskWriteBps).toBe(0);
    expect(a?.netOutBps).toBe(0);
  });

  it("computes BPS from the cumulative counter delta over wall time", () => {
    useMetricStore.getState().processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:00Z",
        diskReadA: 0,
        diskReadB: 0,
        netInA: 0,
      }),
    );
    // Advance the clock by exactly 10 seconds
    now += 10_000;
    useMetricStore.getState().processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:10Z",
        diskReadA: 5_000_000,
        diskReadB: 5_000_000,
        netInA: 1_000_000,
      }),
    );
    const a = useMetricStore.getState().clusters.get(CLUSTER);
    // 10 MB total disk read in 10 seconds = 1 MB/s
    expect(a?.diskReadBps).toBeCloseTo(1_000_000, 0);
    // 1 MB total net_in in 10 seconds = 100 KB/s
    expect(a?.netInBps).toBeCloseTo(100_000, 0);
  });

  it("clamps negative deltas to zero (counter rollover protection)", () => {
    useMetricStore.getState().processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:00Z",
        diskReadA: 5_000_000,
      }),
    );
    now += 10_000;
    // New cumulative counter is LOWER than the previous (e.g. node restarted)
    useMetricStore.getState().processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:10Z",
        diskReadA: 100,
      }),
    );
    const a = useMetricStore.getState().clusters.get(CLUSTER);
    // Negative delta clamped to 0, not exposed as a negative or NaN
    expect(a?.diskReadBps).toBe(0);
  });
});

describe("metric-store: dedupe by collected_at (race condition fix)", () => {
  let now = 1_700_000_000_000;
  beforeEach(() => {
    now = 1_700_000_000_000;
    jest.spyOn(Date, "now").mockImplementation(() => now);
  });
  afterEach(() => {
    jest.restoreAllMocks();
  });

  it("processes only the first message with a given collected_at", () => {
    // First message — establishes prev snapshot
    useMetricStore.getState().processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:00Z",
        diskReadA: 0,
        diskReadB: 0,
      }),
    );
    // 10 seconds later, real second message with new data
    now += 10_000;
    useMetricStore.getState().processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:10Z",
        diskReadA: 5_000_000,
        diskReadB: 5_000_000,
      }),
    );

    const beforeDupe = useMetricStore.getState().clusters.get(CLUSTER);
    const beforeBps = beforeDupe?.diskReadBps;
    const beforeVersion = useMetricStore.getState().version;

    // Now simulate a second listener processing the SAME payload in the same
    // event loop tick (the race the fix targets — multiple subscribers via
    // ws-store reference counting).
    useMetricStore.getState().processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:10Z", // SAME timestamp
        diskReadA: 5_000_000,
        diskReadB: 5_000_000,
      }),
    );

    const afterDupe = useMetricStore.getState().clusters.get(CLUSTER);
    expect(afterDupe?.diskReadBps).toBe(beforeBps);
    // Version should NOT bump on the dedup'd call
    expect(useMetricStore.getState().version).toBe(beforeVersion);
  });

  it("processes a new message with a different collected_at", () => {
    useMetricStore.getState().processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:00Z",
        cpuA: 0.5,
      }),
    );
    const v1 = useMetricStore.getState().version;
    now += 10_000;
    useMetricStore.getState().processMessage(
      CLUSTER,
      makePayload({
        collectedAt: "2026-04-08T12:00:10Z",
        cpuA: 0.75,
      }),
    );
    expect(useMetricStore.getState().version).toBe(v1 + 1);
    // Aggregated value reflects the second message
    const a = useMetricStore.getState().clusters.get(CLUSTER);
    expect(a?.cpuPercent).toBeCloseTo((0.75 + 0.25) / 2 * 100, 4);
  });
});

describe("metric-store: lifecycle", () => {
  it("clearCluster removes only the targeted cluster", () => {
    const store = useMetricStore.getState();
    const otherCluster = "99999999-9999-9999-9999-999999999999";
    store.processMessage(
      CLUSTER,
      makePayload({ collectedAt: "2026-04-08T12:00:00Z" }),
    );
    store.processMessage(
      otherCluster,
      makePayload({ collectedAt: "2026-04-08T12:00:00Z" }),
    );

    expect(useMetricStore.getState().clusters.size).toBe(2);
    store.clearCluster(CLUSTER);
    expect(useMetricStore.getState().clusters.has(CLUSTER)).toBe(false);
    expect(useMetricStore.getState().clusters.has(otherCluster)).toBe(true);
  });

  it("clearAll wipes every Map and resets version", () => {
    useMetricStore.getState().processMessage(
      CLUSTER,
      makePayload({ collectedAt: "2026-04-08T12:00:00Z" }),
    );
    expect(useMetricStore.getState().clusters.size).toBeGreaterThan(0);
    expect(useMetricStore.getState().vms.size).toBeGreaterThan(0);

    useMetricStore.getState().clearAll();

    const state = useMetricStore.getState();
    expect(state.clusters.size).toBe(0);
    expect(state.vms.size).toBe(0);
    expect(state.nodes.size).toBe(0);
    expect(state.prevSnapshots.size).toBe(0);
    expect(state.lastCollectedAt.size).toBe(0);
    expect(state.version).toBe(0);
  });
});
