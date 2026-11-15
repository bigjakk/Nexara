import type { LayoutItem } from "react-grid-layout";

export interface WidgetTemplate {
  type: string;
  label: string;
  description: string;
  defaultLayout: { w: number; h: number; minW?: number; minH?: number };
  category: "overview" | "metrics" | "cluster";
  /** If true, one instance is created per cluster */
  perCluster: boolean;
  /**
   * The global grant the widget's data needs, as "action:resource". The
   * picker leaves the widget out without it, and the dashboard renders a note
   * in its place, since its API would refuse the reads.
   */
  requiresGlobal?: string;
}

export const widgetTemplates: WidgetTemplate[] = [
  {
    type: "fleet-board",
    label: "Fleet",
    description: "Every node with its load and one cell per guest",
    defaultLayout: { w: 12, h: 10, minW: 6, minH: 5 },
    category: "overview",
    perCluster: false,
  },
  {
    type: "stats-overview",
    label: "All clusters",
    description: "Nodes, guests, CPU, memory and storage across every cluster",
    defaultLayout: { w: 12, h: 4, minW: 6, minH: 3 },
    category: "overview",
    perCluster: false,
  },
  {
    type: "cluster-cards",
    label: "Clusters",
    description: "Each cluster's state, contents and how full it is",
    defaultLayout: { w: 12, h: 5, minW: 6, minH: 3 },
    category: "cluster",
    perCluster: false,
  },
  {
    type: "cpu-chart",
    label: "CPU usage chart",
    description: "CPU usage over time",
    defaultLayout: { w: 6, h: 8, minW: 4, minH: 6 },
    category: "metrics",
    perCluster: true,
  },
  {
    type: "memory-chart",
    label: "Memory usage chart",
    description: "Memory usage over time",
    defaultLayout: { w: 6, h: 8, minW: 4, minH: 6 },
    category: "metrics",
    perCluster: true,
  },
  {
    type: "disk-chart",
    label: "Disk I/O chart",
    description: "Disk I/O read rate over time",
    defaultLayout: { w: 6, h: 8, minW: 4, minH: 6 },
    category: "metrics",
    perCluster: true,
  },
  {
    type: "network-chart",
    label: "Network in chart",
    description: "Network inbound traffic over time",
    defaultLayout: { w: 6, h: 8, minW: 4, minH: 6 },
    category: "metrics",
    perCluster: true,
  },
  {
    type: "live-metrics",
    label: "Live load",
    description: "CPU, memory, disk and network right now",
    defaultLayout: { w: 12, h: 4, minW: 6, minH: 3 },
    category: "metrics",
    perCluster: true,
  },
  {
    type: "top-consumers",
    label: "Top consumers",
    description: "The guests using the most CPU or memory",
    defaultLayout: { w: 12, h: 5, minW: 6, minH: 3 },
    category: "metrics",
    perCluster: false,
  },
  {
    type: "ups-status",
    label: "UPS status",
    description: "Each UPS's status, charge and runtime left",
    defaultLayout: { w: 6, h: 5, minW: 4, minH: 4 },
    category: "overview",
    perCluster: false,
    requiresGlobal: "view:ups",
  },
];

/** Whether the caller may have a widget, by its template's requiresGlobal. */
export function widgetAllowed(
  template: WidgetTemplate,
  hasGlobalPermission: (action: string, resource: string) => boolean,
): boolean {
  if (template.requiresGlobal === undefined) return true;
  const [action = "", resource = ""] = template.requiresGlobal.split(":");
  return hasGlobalPermission(action, resource);
}

/** Parse a widget ID like "cpu-chart:abc123" into { type, clusterId } */
export function parseWidgetId(widgetId: string): {
  type: string;
  clusterId: string | null;
} {
  const idx = widgetId.indexOf(":");
  if (idx === -1) return { type: widgetId, clusterId: null };
  return {
    type: widgetId.substring(0, idx),
    clusterId: widgetId.substring(idx + 1),
  };
}

/** Build a widget ID from type and optional clusterId */
export function buildWidgetId(type: string, clusterId: string | null): string {
  return clusterId ? `${type}:${clusterId}` : type;
}

/** Get the template for a widget ID */
export function getTemplate(widgetId: string): WidgetTemplate | undefined {
  const { type } = parseWidgetId(widgetId);
  return widgetTemplates.find((t) => t.type === type);
}

/** Get the display label for a widget instance */
export function getWidgetLabel(
  widgetId: string,
  clusterNames: Map<string, string>,
): string {
  const { type, clusterId } = parseWidgetId(widgetId);
  const template = widgetTemplates.find((t) => t.type === type);
  if (!template) return widgetId;
  if (clusterId) {
    const name = clusterNames.get(clusterId) ?? "Cluster";
    return `${name} — ${template.label}`;
  }
  return template.label;
}

export interface ClusterInfo {
  id: string;
  name: string;
  /** Sizes the Fleet widget in a default layout; unknown counts as none. */
  nodeCount?: number;
}

/**
 * Grid rows the Fleet widget needs to show every node without scrolling: a
 * header, a line per cluster and a row per node, at the grid's 30px row and
 * 16px gap. Clamped, because past that it scrolls inside its cell instead.
 */
export function fleetBoardRows(clusters: ClusterInfo[]): number {
  const px = clusters.reduce(
    (sum, c) => sum + 54 + 40 * (c.nodeCount ?? 0),
    96,
  );
  return Math.min(20, Math.max(5, Math.ceil((px + 16) / 46)));
}

function makeLayoutItem(
  id: string,
  x: number,
  y: number,
  dl: WidgetTemplate["defaultLayout"],
): LayoutItem {
  const item: LayoutItem = { i: id, x, y, w: dl.w, h: dl.h };
  if (dl.minW != null) item.minW = dl.minW;
  if (dl.minH != null) item.minH = dl.minH;
  return item;
}

/** Generate the default widget IDs for the given set of clusters */
export function getDefaultWidgetIds(clusters: ClusterInfo[]): string[] {
  // Fleet stands in for the stat and cluster cards, which stay in the picker.
  const ids: string[] = ["fleet-board"];

  for (const cluster of clusters) {
    ids.push(`cpu-chart:${cluster.id}`);
    ids.push(`memory-chart:${cluster.id}`);
  }

  for (const cluster of clusters) {
    ids.push(`disk-chart:${cluster.id}`);
    ids.push(`network-chart:${cluster.id}`);
  }

  for (const cluster of clusters) {
    ids.push(`live-metrics:${cluster.id}`);
  }

  ids.push("top-consumers");
  return ids;
}

/** Generate default layout for the given widget IDs */
export function getDefaultLayout(
  widgetIds: string[],
  clusters: ClusterInfo[] = [],
): LayoutItem[] {
  let y = 0;
  const layouts: LayoutItem[] = [];

  for (const widgetId of widgetIds) {
    const template = getTemplate(widgetId);
    if (!template) continue;

    const dl =
      template.type === "fleet-board"
        ? { ...template.defaultLayout, h: fleetBoardRows(clusters) }
        : template.defaultLayout;
    const isHalfWidth = dl.w <= 6;
    const prevItem = layouts[layouts.length - 1];

    if (
      isHalfWidth &&
      prevItem !== undefined &&
      prevItem.w <= 6 &&
      prevItem.y === y
    ) {
      layouts.push(makeLayoutItem(widgetId, 6, y, dl));
      y += Math.max(dl.h, prevItem.h);
    } else {
      if (layouts.length > 0) {
        const prev = layouts[layouts.length - 1];
        if (prev !== undefined) {
          y = prev.y + prev.h;
        }
      }
      layouts.push(makeLayoutItem(widgetId, 0, y, dl));
      if (!isHalfWidth) {
        y += dl.h;
      }
    }
  }

  return layouts;
}

/** Get all possible widget instances that could be added, given current clusters */
export function getAllAvailableWidgets(clusters: ClusterInfo[]): {
  id: string;
  label: string;
  description: string;
  template: WidgetTemplate;
}[] {
  const result: {
    id: string;
    label: string;
    description: string;
    template: WidgetTemplate;
  }[] = [];

  for (const template of widgetTemplates) {
    if (template.perCluster) {
      for (const cluster of clusters) {
        const id = buildWidgetId(template.type, cluster.id);
        result.push({
          id,
          label: `${cluster.name} — ${template.label}`,
          description: template.description,
          template,
        });
      }
    } else {
      result.push({
        id: template.type,
        label: template.label,
        description: template.description,
        template,
      });
    }
  }

  return result;
}

/**
 * The default layout's widgets that a saved layout has not placed and its
 * owner has not been offered before. The dashboard offers these; it never adds
 * them to a saved layout itself.
 */
export function unplacedDefaultWidgets(
  placed: readonly string[],
  defaults: readonly string[],
  offeredBefore: readonly string[],
): string[] {
  return defaults.filter(
    (id) => !placed.includes(id) && !offeredBefore.includes(id),
  );
}

/**
 * `preset` with `ids` added below everything already in it, each at its
 * template's default size. What is there keeps its place and size.
 */
export function appendWidgets(
  preset: DashboardPreset,
  ids: readonly string[],
  clusters: ClusterInfo[],
): DashboardPreset {
  let y = preset.layouts.reduce((max, l) => Math.max(max, l.y + l.h), 0);
  const added: LayoutItem[] = [];
  const addedIds: string[] = [];
  for (const id of ids) {
    const template = getTemplate(id);
    if (!template || preset.widgetIds.includes(id)) continue;
    const dl =
      template.type === "fleet-board"
        ? { ...template.defaultLayout, h: fleetBoardRows(clusters) }
        : template.defaultLayout;
    added.push(makeLayoutItem(id, 0, y, dl));
    addedIds.push(id);
    y += dl.h;
  }
  return {
    ...preset,
    widgetIds: [...preset.widgetIds, ...addedIds],
    layouts: [...preset.layouts, ...added],
  };
}

export interface DashboardPreset {
  name: string;
  widgetIds: string[];
  layouts: LayoutItem[];
}

/** Build a default preset for the given clusters */
export function buildDefaultPreset(clusters: ClusterInfo[]): DashboardPreset {
  const widgetIds = getDefaultWidgetIds(clusters);
  return {
    name: "Default",
    widgetIds,
    layouts: getDefaultLayout(widgetIds, clusters),
  };
}

// Keep a static fallback for when clusters aren't loaded yet
export const defaultPreset: DashboardPreset = buildDefaultPreset([]);
