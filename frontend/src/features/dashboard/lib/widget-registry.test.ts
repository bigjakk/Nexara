import { describe, it, expect } from "vitest";
import {
  widgetTemplates,
  parseWidgetId,
  buildWidgetId,
  getTemplate,
  getWidgetLabel,
  getDefaultWidgetIds,
  getDefaultLayout,
  getAllAvailableWidgets,
  buildDefaultPreset,
  defaultPreset,
  type ClusterInfo,
} from "./widget-registry";

const testClusters: ClusterInfo[] = [
  { id: "c1", name: "Prod Cluster" },
  { id: "c2", name: "Dev Cluster" },
];

describe("widgetTemplates", () => {
  it("contains at least one template", () => {
    expect(widgetTemplates.length).toBeGreaterThan(0);
  });

  it("contains expected template types", () => {
    const types = widgetTemplates.map((t) => t.type);
    expect(types).toContain("stats-overview");
    expect(types).toContain("cluster-cards");
    expect(types).toContain("cpu-chart");
    expect(types).toContain("memory-chart");
    expect(types).toContain("disk-chart");
    expect(types).toContain("network-chart");
    expect(types).toContain("live-metrics");
    expect(types).toContain("top-consumers");
  });

  it("each template has required fields", () => {
    for (const t of widgetTemplates) {
      expect(typeof t.type).toBe("string");
      expect(t.type.length).toBeGreaterThan(0);
      expect(typeof t.label).toBe("string");
      expect(typeof t.description).toBe("string");
      expect(t.defaultLayout.w).toBeGreaterThan(0);
      expect(t.defaultLayout.h).toBeGreaterThan(0);
      expect(["overview", "metrics", "cluster"]).toContain(t.category);
      expect(typeof t.perCluster).toBe("boolean");
    }
  });

  it("all template types are unique", () => {
    const types = widgetTemplates.map((t) => t.type);
    expect(new Set(types).size).toBe(types.length);
  });

  it("per-cluster templates include chart types and live-metrics", () => {
    const perCluster = widgetTemplates.filter((t) => t.perCluster);
    const types = perCluster.map((t) => t.type);
    expect(types).toContain("cpu-chart");
    expect(types).toContain("memory-chart");
    expect(types).toContain("live-metrics");
  });

  it("global templates include stats-overview and cluster-cards", () => {
    const global = widgetTemplates.filter((t) => !t.perCluster);
    const types = global.map((t) => t.type);
    expect(types).toContain("stats-overview");
    expect(types).toContain("cluster-cards");
  });
});

describe("parseWidgetId", () => {
  it("parses global widget ID", () => {
    expect(parseWidgetId("stats-overview")).toEqual({ type: "stats-overview", clusterId: null });
  });

  it("parses per-cluster widget ID", () => {
    expect(parseWidgetId("cpu-chart:abc123")).toEqual({ type: "cpu-chart", clusterId: "abc123" });
  });

  it("handles widget ID with UUID cluster ID", () => {
    const id = "memory-chart:550e8400-e29b-41d4-a716-446655440000";
    const result = parseWidgetId(id);
    expect(result.type).toBe("memory-chart");
    expect(result.clusterId).toBe("550e8400-e29b-41d4-a716-446655440000");
  });
});

describe("buildWidgetId", () => {
  it("builds global widget ID", () => {
    expect(buildWidgetId("stats-overview", null)).toBe("stats-overview");
  });

  it("builds per-cluster widget ID", () => {
    expect(buildWidgetId("cpu-chart", "abc")).toBe("cpu-chart:abc");
  });
});

describe("getTemplate", () => {
  it("returns template for global widget ID", () => {
    const t = getTemplate("stats-overview");
    expect(t).toBeDefined();
    expect(t?.type).toBe("stats-overview");
  });

  it("returns template for per-cluster widget ID", () => {
    const t = getTemplate("cpu-chart:abc");
    expect(t).toBeDefined();
    expect(t?.type).toBe("cpu-chart");
  });

  it("returns undefined for unknown type", () => {
    expect(getTemplate("unknown-widget")).toBeUndefined();
  });
});

describe("getWidgetLabel", () => {
  const names = new Map([["c1", "Prod"], ["c2", "Dev"]]);

  it("returns template label for global widget", () => {
    expect(getWidgetLabel("stats-overview", names)).toBe("Stats Overview");
  });

  it("returns cluster-prefixed label for per-cluster widget", () => {
    expect(getWidgetLabel("cpu-chart:c1", names)).toBe("Prod — CPU Usage Chart");
  });

  it("falls back to 'Cluster' if cluster name not found", () => {
    expect(getWidgetLabel("cpu-chart:unknown", names)).toBe("Cluster — CPU Usage Chart");
  });
});

describe("getDefaultWidgetIds", () => {
  it("includes global widgets", () => {
    const ids = getDefaultWidgetIds(testClusters);
    expect(ids).toContain("stats-overview");
    expect(ids).toContain("cluster-cards");
    expect(ids).toContain("top-consumers");
  });

  it("includes per-cluster widgets for each cluster", () => {
    const ids = getDefaultWidgetIds(testClusters);
    expect(ids).toContain("cpu-chart:c1");
    expect(ids).toContain("cpu-chart:c2");
    expect(ids).toContain("memory-chart:c1");
    expect(ids).toContain("memory-chart:c2");
    expect(ids).toContain("live-metrics:c1");
    expect(ids).toContain("live-metrics:c2");
  });

  it("returns only global widgets when no clusters", () => {
    const ids = getDefaultWidgetIds([]);
    expect(ids).toContain("stats-overview");
    expect(ids).toContain("cluster-cards");
    expect(ids).toContain("top-consumers");
    expect(ids.some((id) => id.includes(":"))).toBe(false);
  });

  it("all IDs are unique", () => {
    const ids = getDefaultWidgetIds(testClusters);
    expect(new Set(ids).size).toBe(ids.length);
  });
});

describe("getDefaultLayout", () => {
  it("returns a layout item for every widget ID", () => {
    const ids = getDefaultWidgetIds(testClusters);
    const layouts = getDefaultLayout(ids);
    expect(layouts).toHaveLength(ids.length);
  });

  it("each layout item has valid fields", () => {
    const ids = getDefaultWidgetIds(testClusters);
    const layouts = getDefaultLayout(ids);
    for (const item of layouts) {
      expect(typeof item.i).toBe("string");
      expect(typeof item.x).toBe("number");
      expect(typeof item.y).toBe("number");
      expect(item.w).toBeGreaterThan(0);
      expect(item.h).toBeGreaterThan(0);
    }
  });

  it("layout positions are non-negative", () => {
    const ids = getDefaultWidgetIds(testClusters);
    const layouts = getDefaultLayout(ids);
    for (const item of layouts) {
      expect(item.x).toBeGreaterThanOrEqual(0);
      expect(item.y).toBeGreaterThanOrEqual(0);
    }
  });

  it("half-width widgets are placed side-by-side", () => {
    const ids = getDefaultWidgetIds(testClusters);
    const layouts = getDefaultLayout(ids);
    const halfWidth = layouts.filter((l) => l.w <= 6);
    const yValues = halfWidth.map((l) => l.y);
    const rowCounts = new Map<number, number>();
    for (const y of yValues) {
      rowCounts.set(y, (rowCounts.get(y) ?? 0) + 1);
    }
    const hasPairedRow = [...rowCounts.values()].some((count) => count >= 2);
    expect(hasPairedRow).toBe(true);
  });
});

describe("getAllAvailableWidgets", () => {
  it("returns global widgets", () => {
    const available = getAllAvailableWidgets(testClusters);
    const ids = available.map((w) => w.id);
    expect(ids).toContain("stats-overview");
    expect(ids).toContain("cluster-cards");
  });

  it("returns per-cluster widgets for each cluster", () => {
    const available = getAllAvailableWidgets(testClusters);
    const ids = available.map((w) => w.id);
    expect(ids).toContain("cpu-chart:c1");
    expect(ids).toContain("cpu-chart:c2");
  });

  it("labels include cluster name for per-cluster widgets", () => {
    const available = getAllAvailableWidgets(testClusters);
    const cpuC1 = available.find((w) => w.id === "cpu-chart:c1");
    expect(cpuC1?.label).toContain("Prod Cluster");
  });

  it("returns more widgets with more clusters", () => {
    const first = testClusters[0];
    if (!first) throw new Error("test setup error");
    const one = getAllAvailableWidgets([first]);
    const two = getAllAvailableWidgets(testClusters);
    expect(two.length).toBeGreaterThan(one.length);
  });
});

describe("buildDefaultPreset", () => {
  it("has name 'Default'", () => {
    const preset = buildDefaultPreset(testClusters);
    expect(preset.name).toBe("Default");
  });

  it("layouts match widgetIds count", () => {
    const preset = buildDefaultPreset(testClusters);
    expect(preset.layouts).toHaveLength(preset.widgetIds.length);
  });

  it("includes per-cluster widgets", () => {
    const preset = buildDefaultPreset(testClusters);
    expect(preset.widgetIds.some((id) => id.includes("c1"))).toBe(true);
    expect(preset.widgetIds.some((id) => id.includes("c2"))).toBe(true);
  });
});

describe("defaultPreset (static fallback)", () => {
  it("has name 'Default'", () => {
    expect(defaultPreset.name).toBe("Default");
  });

  it("has global widgets only (no clusters)", () => {
    expect(defaultPreset.widgetIds).toContain("stats-overview");
    expect(defaultPreset.widgetIds.every((id) => !id.includes(":"))).toBe(true);
  });
});
