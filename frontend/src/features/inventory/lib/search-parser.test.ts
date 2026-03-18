import { describe, it, expect } from "vitest";
import { parseQuery, applyFilter } from "./search-parser";
import type { InventoryRow } from "../types/inventory";

function makeRow(overrides: Partial<InventoryRow> = {}): InventoryRow {
  return {
    key: "c1:vm:1",
    id: "vm-1",
    type: "vm",
    name: "web-server-01",
    status: "running",
    clusterName: "Production",
    clusterId: "c1",
    nodeName: "node1",
    vmid: 100,
    cpuCount: 4,
    memTotal: 8589934592,
    diskTotal: 107374182400,
    uptime: 86400,
    tags: "web,prod",
    haState: "started",
    pool: "webpool",
    template: false,
    cpuPercent: 45,
    memPercent: 60,
    ...overrides,
  };
}

describe("parseQuery", () => {
  it("parses empty string", () => {
    const result = parseQuery("");
    expect(result.filters).toHaveLength(0);
    expect(result.freeText).toBe("");
  });

  it("parses free text only", () => {
    const result = parseQuery("web server");
    expect(result.filters).toHaveLength(0);
    expect(result.freeText).toBe("web server");
  });

  it("parses type:vm filter", () => {
    const result = parseQuery("type:vm");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "type",
      operator: "eq",
      value: "vm",
    });
  });

  it("parses status:running filter", () => {
    const result = parseQuery("status:running");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "status",
      operator: "eq",
      value: "running",
    });
  });

  it("parses cpu>80% comparison", () => {
    const result = parseQuery("cpu>80%");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "cpu",
      operator: "gt",
      value: "80",
    });
  });

  it("parses mem<50% comparison", () => {
    const result = parseQuery("mem<50%");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "mem",
      operator: "lt",
      value: "50",
    });
  });

  it("parses comparison without % sign", () => {
    const result = parseQuery("cpu>90");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "cpu",
      operator: "gt",
      value: "90",
    });
  });

  it("parses mixed filters and free text", () => {
    const result = parseQuery("type:vm status:running web-server cpu>80%");
    expect(result.filters).toHaveLength(3);
    expect(result.freeText).toBe("web-server");
  });

  it("ignores unknown fields as free text", () => {
    const result = parseQuery("foo:bar baz");
    expect(result.filters).toHaveLength(0);
    expect(result.freeText).toBe("foo:bar baz");
  });

  it("handles case-insensitive field names", () => {
    const result = parseQuery("Type:VM Status:Running");
    expect(result.filters).toHaveLength(2);
    expect(result.filters[0]?.field).toBe("type");
    expect(result.filters[0]?.value).toBe("vm");
  });

  // --- New: negation ---
  it("parses negated filter !status:stopped", () => {
    const result = parseQuery("!status:stopped");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "status",
      operator: "neq",
      value: "stopped",
    });
  });

  it("parses negated type filter !type:node", () => {
    const result = parseQuery("!type:node");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "type",
      operator: "neq",
      value: "node",
    });
  });

  // --- New: comma-separated OR values ---
  it("parses comma-separated values status:running,paused", () => {
    const result = parseQuery("status:running,paused");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "status",
      operator: "eq",
      value: "running,paused",
    });
  });

  it("parses negated comma values !type:vm,ct", () => {
    const result = parseQuery("!type:vm,ct");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "type",
      operator: "neq",
      value: "vm,ct",
    });
  });

  // --- New: quoted values ---
  it('parses quoted value name:"my server"', () => {
    const result = parseQuery('name:"my server"');
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "name",
      operator: "eq",
      value: "my server",
    });
  });

  // --- New: uptime duration ---
  it("parses uptime>1d as seconds", () => {
    const result = parseQuery("uptime>1d");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "uptime",
      operator: "gt",
      value: "86400",
    });
  });

  it("parses uptime<2h as seconds", () => {
    const result = parseQuery("uptime<2h");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "uptime",
      operator: "lt",
      value: "7200",
    });
  });

  it("parses uptime>30m as seconds", () => {
    const result = parseQuery("uptime>30m");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "uptime",
      operator: "gt",
      value: "1800",
    });
  });

  it("parses uptime>1w as seconds", () => {
    const result = parseQuery("uptime>1w");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "uptime",
      operator: "gt",
      value: "604800",
    });
  });

  // --- New: cpus/cores ---
  it("parses cpus>4 filter", () => {
    const result = parseQuery("cpus>4");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "cpus",
      operator: "gt",
      value: "4",
    });
  });

  it("parses cores<8 filter", () => {
    const result = parseQuery("cores<8");
    expect(result.filters).toHaveLength(1);
    expect(result.filters[0]).toEqual({
      field: "cores",
      operator: "lt",
      value: "8",
    });
  });
});

describe("applyFilter", () => {
  const rows: InventoryRow[] = [
    makeRow({
      key: "c1:vm:1",
      name: "web-server-01",
      type: "vm",
      status: "running",
      cpuPercent: 85,
      memPercent: 60,
      clusterName: "Production",
      nodeName: "pve1",
      vmid: 100,
      cpuCount: 4,
      uptime: 172800, // 2 days
      tags: "web,prod",
    }),
    makeRow({
      key: "c1:vm:2",
      name: "db-server-01",
      type: "vm",
      status: "stopped",
      cpuPercent: 0,
      memPercent: 10,
      clusterName: "Production",
      nodeName: "pve2",
      vmid: 101,
      cpuCount: 8,
      uptime: 0,
      tags: "database,prod",
    }),
    makeRow({
      key: "c1:ct:1",
      name: "dns-container",
      type: "ct",
      status: "running",
      cpuPercent: 15,
      memPercent: 30,
      clusterName: "Staging",
      nodeName: "pve1",
      vmid: 200,
      cpuCount: 2,
      uptime: 3600, // 1 hour
      tags: "dns,staging",
    }),
    makeRow({
      key: "c1:node:1",
      name: "pve1",
      type: "node",
      status: "online",
      cpuPercent: 55,
      memPercent: 70,
      clusterName: "Production",
      nodeName: "pve1",
      vmid: null,
      cpuCount: 16,
      uptime: 604800, // 1 week
      tags: "",
    }),
  ];

  it("returns all rows with empty query", () => {
    const parsed = parseQuery("");
    expect(applyFilter(rows, parsed)).toHaveLength(4);
  });

  it("filters by type:vm", () => {
    const parsed = parseQuery("type:vm");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(2);
    expect(result.every((r) => r.type === "vm")).toBe(true);
  });

  it("filters by type:ct", () => {
    const parsed = parseQuery("type:ct");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.type).toBe("ct");
  });

  it("filters by type alias lxc", () => {
    const parsed = parseQuery("type:lxc");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.type).toBe("ct");
  });

  it("filters by status", () => {
    const parsed = parseQuery("status:running");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(2);
  });

  it("filters by cpu>80%", () => {
    const parsed = parseQuery("cpu>80%");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("web-server-01");
  });

  it("filters by mem<50%", () => {
    const parsed = parseQuery("mem<50%");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(2);
  });

  it("applies free text name search", () => {
    const parsed = parseQuery("web");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("web-server-01");
  });

  it("combines multiple filters with AND logic", () => {
    const parsed = parseQuery("type:vm status:running");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("web-server-01");
  });

  it("combines filter and free text", () => {
    const parsed = parseQuery("type:vm db");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("db-server-01");
  });

  it("handles null metric values gracefully", () => {
    const rowsWithNull = [makeRow({ cpuPercent: null, memPercent: null })];
    const parsed = parseQuery("cpu>50%");
    const result = applyFilter(rowsWithNull, parsed);
    expect(result).toHaveLength(0);
  });

  // --- New: free text searches cluster name ---
  it("free text matches cluster name", () => {
    const parsed = parseQuery("staging");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.clusterName).toBe("Staging");
  });

  // --- New: free text searches node name ---
  it("free text matches node name", () => {
    const parsed = parseQuery("pve2");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.nodeName).toBe("pve2");
  });

  // --- New: free text searches vmid ---
  it("free text matches vmid", () => {
    const parsed = parseQuery("200");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("dns-container");
  });

  // --- New: free text searches tags ---
  it("free text matches tags", () => {
    const parsed = parseQuery("database");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("db-server-01");
  });

  // --- New: negation ---
  it("negated filter !status:stopped excludes stopped", () => {
    const parsed = parseQuery("!status:stopped");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(3);
    expect(result.every((r) => r.status !== "stopped")).toBe(true);
  });

  it("negated filter !type:node excludes nodes", () => {
    const parsed = parseQuery("!type:node");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(3);
    expect(result.every((r) => r.type !== "node")).toBe(true);
  });

  // --- New: comma OR values ---
  it("comma values status:running,stopped matches both", () => {
    const parsed = parseQuery("status:running,stopped");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(3); // web-server, db-server, dns-container
  });

  it("comma values type:vm,ct matches both types", () => {
    const parsed = parseQuery("type:vm,ct");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(3);
    expect(result.every((r) => r.type === "vm" || r.type === "ct")).toBe(true);
  });

  it("negated comma values !type:vm,ct excludes both types", () => {
    const parsed = parseQuery("!type:vm,ct");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.type).toBe("node");
  });

  // --- New: quoted values ---
  it('quoted value name:"web-server" matches', () => {
    const parsed = parseQuery('name:"web-server"');
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("web-server-01");
  });

  // --- New: uptime duration ---
  it("uptime>1d filters by uptime in seconds", () => {
    const parsed = parseQuery("uptime>1d");
    const result = applyFilter(rows, parsed);
    // web-server: 172800 (2d) > 86400 ✓, pve1: 604800 (1w) > 86400 ✓
    expect(result).toHaveLength(2);
  });

  it("uptime<1h filters short-running resources", () => {
    const parsed = parseQuery("uptime<1h");
    const result = applyFilter(rows, parsed);
    // db-server: 0 < 3600 ✓
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("db-server-01");
  });

  // --- New: cpus/cores ---
  it("cpus>4 filters by CPU count", () => {
    const parsed = parseQuery("cpus>4");
    const result = applyFilter(rows, parsed);
    // db-server: 8 > 4 ✓, pve1: 16 > 4 ✓
    expect(result).toHaveLength(2);
  });

  it("cores<4 filters by CPU count", () => {
    const parsed = parseQuery("cores<4");
    const result = applyFilter(rows, parsed);
    // dns-container: 2 < 4 ✓
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("dns-container");
  });

  // --- New: combined advanced queries ---
  it("complex query: type:vm !status:stopped cpu>10%", () => {
    const parsed = parseQuery("type:vm !status:stopped cpu>10%");
    const result = applyFilter(rows, parsed);
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("web-server-01");
  });
});
