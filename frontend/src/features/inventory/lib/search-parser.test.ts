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
});

describe("applyFilter", () => {
  const rows: InventoryRow[] = [
    makeRow({ key: "c1:vm:1", name: "web-server-01", type: "vm", status: "running", cpuPercent: 85, memPercent: 60 }),
    makeRow({ key: "c1:vm:2", name: "db-server-01", type: "vm", status: "stopped", cpuPercent: 0, memPercent: 10 }),
    makeRow({ key: "c1:ct:1", name: "dns-container", type: "ct", status: "running", cpuPercent: 15, memPercent: 30 }),
    makeRow({ key: "c1:node:1", name: "node1", type: "node", status: "online", cpuPercent: 55, memPercent: 70, vmid: null }),
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
    const rowsWithNull = [
      makeRow({ cpuPercent: null, memPercent: null }),
    ];
    const parsed = parseQuery("cpu>50%");
    const result = applyFilter(rowsWithNull, parsed);
    expect(result).toHaveLength(0);
  });
});
