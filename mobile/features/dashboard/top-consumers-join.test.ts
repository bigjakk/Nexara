/**
 * Tests for the top-consumers data join. Pure function — no React, no
 * mocks, no Zustand. The join is the most logic-heavy piece of the
 * dashboard widget; everything else is rendering and TanStack Query
 * plumbing.
 */

import { describe, expect, it } from "@jest/globals";

import type { Cluster, VM } from "@/features/api/types";
import type { LiveResourceMetric } from "@/stores/metric-store";

import { joinTopConsumers } from "./top-consumers-join";

// Test fixtures — minimal VM and Cluster shapes (the join only uses
// id / name / type / template / vmid; everything else is filler).
function makeVM(id: string, name: string, opts: Partial<VM> = {}): VM {
  return {
    id,
    name,
    cluster_id: "",
    node_id: "",
    vmid: 100,
    type: "qemu",
    status: "running",
    cpu_count: 2,
    mem_total: 4 * 1024 * 1024 * 1024,
    disk_total: 32 * 1024 * 1024 * 1024,
    uptime: 12345,
    template: false,
    tags: "",
    ha_state: "",
    pool: "",
    last_seen_at: "2026-04-08T12:00:00Z",
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-08T12:00:00Z",
    ...opts,
  };
}

function makeCluster(id: string, name: string): Cluster {
  return {
    id,
    name,
    api_url: "https://example.com",
    token_id: "root@pam!nexara",
    tls_fingerprint: "",
    sync_interval_seconds: 30,
    is_active: true,
    status: "online",
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-08T12:00:00Z",
  };
}

function liveMap(
  entries: [string, number, number][],
): Map<string, LiveResourceMetric> {
  const m = new Map<string, LiveResourceMetric>();
  for (const [vmId, cpu, mem] of entries) {
    m.set(vmId, {
      cpuPercent: cpu,
      memPercent: mem,
      memUsed: 0,
      memTotal: 0,
    });
  }
  return m;
}

describe("joinTopConsumers", () => {
  it("returns empty list when there's no overlap between VM lists and live metrics", () => {
    const clusters = [makeCluster("c1", "alpha")];
    const vms = [[makeVM("vm-a", "vm-1")]];
    const live = liveMap([["vm-different", 50, 50]]);
    expect(joinTopConsumers(clusters, vms, live)).toEqual([]);
  });

  it("joins live metrics with VM metadata across multiple clusters", () => {
    const clusters = [makeCluster("c1", "alpha"), makeCluster("c2", "beta")];
    const vms = [
      [makeVM("vm-a", "web", { vmid: 100 })],
      [makeVM("vm-b", "db", { vmid: 200 })],
    ];
    const live = liveMap([
      ["vm-a", 75, 60],
      ["vm-b", 30, 40],
    ]);

    const result = joinTopConsumers(clusters, vms, live);

    expect(result).toHaveLength(2);
    // Higher CPU first
    expect(result[0]?.vm.id).toBe("vm-a");
    expect(result[0]?.clusterName).toBe("alpha");
    expect(result[0]?.cpuPercent).toBe(75);
    expect(result[0]?.memPercent).toBe(60);
    expect(result[1]?.vm.id).toBe("vm-b");
    expect(result[1]?.clusterName).toBe("beta");
  });

  it("sorts by CPU% descending", () => {
    const clusters = [makeCluster("c1", "alpha")];
    const vms = [
      [
        makeVM("vm-1", "lo"),
        makeVM("vm-2", "hi"),
        makeVM("vm-3", "mid"),
      ],
    ];
    const live = liveMap([
      ["vm-1", 5, 0],
      ["vm-2", 95, 0],
      ["vm-3", 50, 0],
    ]);

    const result = joinTopConsumers(clusters, vms, live);

    expect(result.map((r) => r.vm.name)).toEqual(["hi", "mid", "lo"]);
  });

  it("filters out templates", () => {
    const clusters = [makeCluster("c1", "alpha")];
    const vms = [
      [
        makeVM("vm-1", "real-vm"),
        makeVM("vm-2", "template-vm", { template: true }),
      ],
    ];
    const live = liveMap([
      ["vm-1", 50, 50],
      ["vm-2", 99, 99], // higher CPU but should be excluded
    ]);

    const result = joinTopConsumers(clusters, vms, live);

    expect(result).toHaveLength(1);
    expect(result[0]?.vm.id).toBe("vm-1");
    expect(result.every((r) => !r.vm.template)).toBe(true);
  });

  it("respects the maxRows parameter and defaults to 10", () => {
    const clusters = [makeCluster("c1", "alpha")];
    const allVMs: VM[] = [];
    const liveEntries: [string, number, number][] = [];
    for (let i = 1; i <= 15; i++) {
      const id = `vm-${String(i)}`;
      allVMs.push(makeVM(id, id));
      liveEntries.push([id, i * 5, 0]); // CPU 5..75
    }
    const vms = [allVMs];
    const live = liveMap(liveEntries);

    expect(joinTopConsumers(clusters, vms, live)).toHaveLength(10);
    expect(joinTopConsumers(clusters, vms, live, 5)).toHaveLength(5);
    expect(joinTopConsumers(clusters, vms, live, 100)).toHaveLength(15);
  });

  it("ignores live VMs that aren't in any cluster's VM list (stale entries)", () => {
    const clusters = [makeCluster("c1", "alpha")];
    const vms = [[makeVM("vm-known", "alive")]];
    const live = liveMap([
      ["vm-known", 30, 30],
      ["vm-stale", 99, 99], // not in any cluster's list
      ["vm-also-stale", 88, 88],
    ]);

    const result = joinTopConsumers(clusters, vms, live);

    expect(result).toHaveLength(1);
    expect(result[0]?.vm.id).toBe("vm-known");
  });

  it("handles undefined entries in vmsByCluster (clusters whose query is still loading)", () => {
    const clusters = [
      makeCluster("c1", "loaded"),
      makeCluster("c2", "loading"),
    ];
    // c1 has data, c2 is still in flight
    const vms: (readonly VM[] | undefined)[] = [
      [makeVM("vm-a", "from-c1")],
      undefined,
    ];
    const live = liveMap([
      ["vm-a", 50, 50],
      ["vm-b", 75, 75], // would belong to c2 but c2's data isn't loaded yet
    ]);

    const result = joinTopConsumers(clusters, vms, live);

    // Only the resolvable VM appears
    expect(result).toHaveLength(1);
    expect(result[0]?.vm.id).toBe("vm-a");
  });

  it("preserves vm.type for navigation (qemu vs lxc)", () => {
    const clusters = [makeCluster("c1", "alpha")];
    const vms = [
      [
        makeVM("vm-q", "qemu-vm", { type: "qemu" }),
        makeVM("vm-l", "lxc-ct", { type: "lxc" }),
      ],
    ];
    const live = liveMap([
      ["vm-q", 50, 50],
      ["vm-l", 60, 60],
    ]);

    const result = joinTopConsumers(clusters, vms, live);
    expect(result.find((r) => r.vm.id === "vm-q")?.vm.type).toBe("qemu");
    expect(result.find((r) => r.vm.id === "vm-l")?.vm.type).toBe("lxc");
  });

  it("handles empty inputs gracefully", () => {
    expect(joinTopConsumers([], [], new Map())).toEqual([]);
    expect(joinTopConsumers([makeCluster("c1", "alpha")], [], new Map())).toEqual(
      [],
    );
    expect(
      joinTopConsumers(
        [makeCluster("c1", "alpha")],
        [[makeVM("vm-1", "test")]],
        new Map(),
      ),
    ).toEqual([]);
  });

  it("includes clusterId in each row for navigation", () => {
    const clusters = [makeCluster("cluster-uuid-1", "prod")];
    const vms = [[makeVM("vm-1", "test")]];
    const live = liveMap([["vm-1", 50, 50]]);

    const result = joinTopConsumers(clusters, vms, live);
    expect(result[0]?.clusterId).toBe("cluster-uuid-1");
  });
});
