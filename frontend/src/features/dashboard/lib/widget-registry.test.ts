import { describe, it, expect } from "vitest";
import {
  widgetRegistry,
  defaultWidgetIds,
  getDefaultLayout,
  defaultPreset,
} from "./widget-registry";
import type { WidgetDefinition } from "./widget-registry";

describe("widgetRegistry", () => {
  it("contains at least one widget", () => {
    expect(widgetRegistry.length).toBeGreaterThan(0);
  });

  it("contains the expected 8 widgets", () => {
    const ids = widgetRegistry.map((w) => w.id);
    expect(ids).toContain("stats-overview");
    expect(ids).toContain("cluster-cards");
    expect(ids).toContain("cpu-chart");
    expect(ids).toContain("memory-chart");
    expect(ids).toContain("disk-chart");
    expect(ids).toContain("network-chart");
    expect(ids).toContain("live-metrics");
    expect(ids).toContain("top-consumers");
    expect(widgetRegistry).toHaveLength(8);
  });

  it("each widget has all required fields", () => {
    for (const widget of widgetRegistry) {
      expect(typeof widget.id).toBe("string");
      expect(widget.id.length).toBeGreaterThan(0);

      expect(typeof widget.label).toBe("string");
      expect(widget.label.length).toBeGreaterThan(0);

      expect(typeof widget.description).toBe("string");
      expect(widget.description.length).toBeGreaterThan(0);

      expect(widget.defaultLayout).toBeDefined();
      expect(typeof widget.defaultLayout.w).toBe("number");
      expect(typeof widget.defaultLayout.h).toBe("number");

      expect(["overview", "metrics", "cluster"]).toContain(widget.category);
    }
  });

  it("each widget has positive layout dimensions", () => {
    for (const widget of widgetRegistry) {
      expect(widget.defaultLayout.w).toBeGreaterThan(0);
      expect(widget.defaultLayout.h).toBeGreaterThan(0);
    }
  });

  it("minW and minH, when present, are less than or equal to w and h", () => {
    for (const widget of widgetRegistry) {
      if (widget.defaultLayout.minW != null) {
        expect(widget.defaultLayout.minW).toBeLessThanOrEqual(
          widget.defaultLayout.w,
        );
      }
      if (widget.defaultLayout.minH != null) {
        expect(widget.defaultLayout.minH).toBeLessThanOrEqual(
          widget.defaultLayout.h,
        );
      }
    }
  });

  it("all widget ids are unique", () => {
    const ids = widgetRegistry.map((w) => w.id);
    const uniqueIds = new Set(ids);
    expect(uniqueIds.size).toBe(ids.length);
  });

  it("widget categories are valid enum values", () => {
    const validCategories: WidgetDefinition["category"][] = [
      "overview",
      "metrics",
      "cluster",
    ];
    for (const widget of widgetRegistry) {
      expect(validCategories).toContain(widget.category);
    }
  });
});

describe("defaultWidgetIds", () => {
  it("matches the number of widgets in the registry", () => {
    expect(defaultWidgetIds).toHaveLength(widgetRegistry.length);
  });

  it("every id in defaultWidgetIds maps to a widget in the registry", () => {
    const registryIds = new Set(widgetRegistry.map((w) => w.id));
    for (const id of defaultWidgetIds) {
      expect(registryIds.has(id)).toBe(true);
    }
  });

  it("all defaultWidgetIds are unique", () => {
    const unique = new Set(defaultWidgetIds);
    expect(unique.size).toBe(defaultWidgetIds.length);
  });
});

describe("getDefaultLayout", () => {
  it("returns a layout item for every defaultWidgetId", () => {
    const layouts = getDefaultLayout();
    expect(layouts).toHaveLength(defaultWidgetIds.length);
  });

  it("each layout item has the expected LayoutItem fields", () => {
    const layouts = getDefaultLayout();
    for (const item of layouts) {
      expect(typeof item.i).toBe("string");
      expect(typeof item.x).toBe("number");
      expect(typeof item.y).toBe("number");
      expect(typeof item.w).toBe("number");
      expect(typeof item.h).toBe("number");
    }
  });

  it("each layout item id corresponds to a known widget", () => {
    const layouts = getDefaultLayout();
    const registryIds = new Set(widgetRegistry.map((w) => w.id));
    for (const item of layouts) {
      expect(registryIds.has(item.i)).toBe(true);
    }
  });

  it("no two layout items share the same widget id", () => {
    const layouts = getDefaultLayout();
    const ids = layouts.map((l) => l.i);
    const unique = new Set(ids);
    expect(unique.size).toBe(ids.length);
  });

  it("layout positions are non-negative", () => {
    const layouts = getDefaultLayout();
    for (const item of layouts) {
      expect(item.x).toBeGreaterThanOrEqual(0);
      expect(item.y).toBeGreaterThanOrEqual(0);
    }
  });

  it("layout dimensions match the widget defaultLayout w and h", () => {
    const layouts = getDefaultLayout();
    for (const item of layouts) {
      const def = widgetRegistry.find((w) => w.id === item.i);
      expect(def).toBeDefined();
      if (!def) continue;
      expect(item.w).toBe(def.defaultLayout.w);
      expect(item.h).toBe(def.defaultLayout.h);
    }
  });

  it("minW and minH are propagated from widget definition when present", () => {
    const layouts = getDefaultLayout();
    for (const item of layouts) {
      const def = widgetRegistry.find((w) => w.id === item.i);
      expect(def).toBeDefined();
      if (!def) continue;
      if (def.defaultLayout.minW != null) {
        expect(item.minW).toBe(def.defaultLayout.minW);
      } else {
        expect(item.minW).toBeUndefined();
      }
      if (def.defaultLayout.minH != null) {
        expect(item.minH).toBe(def.defaultLayout.minH);
      } else {
        expect(item.minH).toBeUndefined();
      }
    }
  });

  it("half-width widgets (w<=6) are placed side-by-side on the same row", () => {
    const layouts = getDefaultLayout();
    const halfWidthLayouts = layouts.filter((l) => l.w <= 6);

    // There should be at least one pair of widgets on the same y row
    const yValues = halfWidthLayouts.map((l) => l.y);
    const rowCounts = new Map<number, number>();
    for (const y of yValues) {
      rowCounts.set(y, (rowCounts.get(y) ?? 0) + 1);
    }
    const hasPairedRow = [...rowCounts.values()].some((count) => count >= 2);
    expect(hasPairedRow).toBe(true);
  });

  it("full-width widgets (w>6) start at x=0", () => {
    const layouts = getDefaultLayout();
    const fullWidthLayouts = layouts.filter((l) => l.w > 6);
    for (const item of fullWidthLayouts) {
      expect(item.x).toBe(0);
    }
  });

  it("returns a new array on each call (referential independence)", () => {
    const first = getDefaultLayout();
    const second = getDefaultLayout();
    expect(first).not.toBe(second);
    expect(first).toEqual(second);
  });
});

describe("defaultPreset", () => {
  it("has a non-empty name", () => {
    expect(typeof defaultPreset.name).toBe("string");
    expect(defaultPreset.name.length).toBeGreaterThan(0);
  });

  it("name is 'Default'", () => {
    expect(defaultPreset.name).toBe("Default");
  });

  it("widgetIds matches defaultWidgetIds", () => {
    expect(defaultPreset.widgetIds).toEqual(defaultWidgetIds);
  });

  it("layouts array has the same length as widgetIds", () => {
    expect(defaultPreset.layouts).toHaveLength(defaultPreset.widgetIds.length);
  });

  it("layouts match the result of getDefaultLayout()", () => {
    expect(defaultPreset.layouts).toEqual(getDefaultLayout());
  });

  it("has widgetIds, layouts, and name properties", () => {
    expect(defaultPreset).toHaveProperty("name");
    expect(defaultPreset).toHaveProperty("widgetIds");
    expect(defaultPreset).toHaveProperty("layouts");
  });

  it("widgetIds is a non-empty array of strings", () => {
    expect(Array.isArray(defaultPreset.widgetIds)).toBe(true);
    expect(defaultPreset.widgetIds.length).toBeGreaterThan(0);
    for (const id of defaultPreset.widgetIds) {
      expect(typeof id).toBe("string");
    }
  });
});
