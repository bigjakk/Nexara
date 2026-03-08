import type { LayoutItem } from "react-grid-layout";

export interface WidgetTemplate {
  type: string;
  label: string;
  description: string;
  defaultLayout: { w: number; h: number; minW?: number; minH?: number };
  category: "overview" | "metrics" | "cluster";
  /** If true, one instance is created per cluster */
  perCluster: boolean;
}

export const widgetTemplates: WidgetTemplate[] = [
  {
    type: "stats-overview",
    label: "Stats Overview",
    description: "Total nodes, VMs, containers, and storage",
    defaultLayout: { w: 12, h: 3, minW: 6, minH: 2 },
    category: "overview",
    perCluster: false,
  },
  {
    type: "cluster-cards",
    label: "Cluster Cards",
    description: "Overview cards for each registered cluster",
    defaultLayout: { w: 12, h: 4, minW: 6, minH: 3 },
    category: "cluster",
    perCluster: false,
  },
  {
    type: "cpu-chart",
    label: "CPU Usage Chart",
    description: "CPU usage over time",
    defaultLayout: { w: 6, h: 5, minW: 4, minH: 4 },
    category: "metrics",
    perCluster: true,
  },
  {
    type: "memory-chart",
    label: "Memory Usage Chart",
    description: "Memory usage over time",
    defaultLayout: { w: 6, h: 5, minW: 4, minH: 4 },
    category: "metrics",
    perCluster: true,
  },
  {
    type: "disk-chart",
    label: "Disk I/O Chart",
    description: "Disk I/O read rate over time",
    defaultLayout: { w: 6, h: 5, minW: 4, minH: 4 },
    category: "metrics",
    perCluster: true,
  },
  {
    type: "network-chart",
    label: "Network In Chart",
    description: "Network inbound traffic over time",
    defaultLayout: { w: 6, h: 5, minW: 4, minH: 4 },
    category: "metrics",
    perCluster: true,
  },
  {
    type: "live-metrics",
    label: "Live Metric Cards",
    description: "Real-time CPU, memory, disk, and network stats",
    defaultLayout: { w: 12, h: 3, minW: 6, minH: 2 },
    category: "metrics",
    perCluster: true,
  },
  {
    type: "top-consumers",
    label: "Top Consumers",
    description: "VMs/containers using the most resources",
    defaultLayout: { w: 12, h: 5, minW: 6, minH: 3 },
    category: "metrics",
    perCluster: false,
  },
];

/** Parse a widget ID like "cpu-chart:abc123" into { type, clusterId } */
export function parseWidgetId(widgetId: string): { type: string; clusterId: string | null } {
  const idx = widgetId.indexOf(":");
  if (idx === -1) return { type: widgetId, clusterId: null };
  return { type: widgetId.substring(0, idx), clusterId: widgetId.substring(idx + 1) };
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
export function getWidgetLabel(widgetId: string, clusterNames: Map<string, string>): string {
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
  const ids: string[] = ["stats-overview", "cluster-cards"];

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
export function getDefaultLayout(widgetIds: string[]): LayoutItem[] {
  let y = 0;
  const layouts: LayoutItem[] = [];

  for (const widgetId of widgetIds) {
    const template = getTemplate(widgetId);
    if (!template) continue;

    const dl = template.defaultLayout;
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
export function getAllAvailableWidgets(clusters: ClusterInfo[]): { id: string; label: string; description: string; template: WidgetTemplate }[] {
  const result: { id: string; label: string; description: string; template: WidgetTemplate }[] = [];

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
    layouts: getDefaultLayout(widgetIds),
  };
}

// Keep a static fallback for when clusters aren't loaded yet
export const defaultPreset: DashboardPreset = buildDefaultPreset([]);
