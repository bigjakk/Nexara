import type { LayoutItem } from "react-grid-layout";

export interface WidgetDefinition {
  id: string;
  label: string;
  description: string;
  defaultLayout: { w: number; h: number; minW?: number; minH?: number };
  category: "overview" | "metrics" | "cluster";
}

export const widgetRegistry: WidgetDefinition[] = [
  {
    id: "stats-overview",
    label: "Stats Overview",
    description: "Total nodes, VMs, containers, and storage",
    defaultLayout: { w: 12, h: 3, minW: 6, minH: 2 },
    category: "overview",
  },
  {
    id: "cluster-cards",
    label: "Cluster Cards",
    description: "Overview cards for each registered cluster",
    defaultLayout: { w: 12, h: 4, minW: 6, minH: 3 },
    category: "cluster",
  },
  {
    id: "cpu-chart",
    label: "CPU Usage Chart",
    description: "CPU usage over time for the first cluster",
    defaultLayout: { w: 6, h: 5, minW: 4, minH: 4 },
    category: "metrics",
  },
  {
    id: "memory-chart",
    label: "Memory Usage Chart",
    description: "Memory usage over time for the first cluster",
    defaultLayout: { w: 6, h: 5, minW: 4, minH: 4 },
    category: "metrics",
  },
  {
    id: "disk-chart",
    label: "Disk I/O Chart",
    description: "Disk I/O read rate over time",
    defaultLayout: { w: 6, h: 5, minW: 4, minH: 4 },
    category: "metrics",
  },
  {
    id: "network-chart",
    label: "Network In Chart",
    description: "Network inbound traffic over time",
    defaultLayout: { w: 6, h: 5, minW: 4, minH: 4 },
    category: "metrics",
  },
  {
    id: "live-metrics",
    label: "Live Metric Cards",
    description: "Real-time CPU, memory, disk, and network stats",
    defaultLayout: { w: 12, h: 3, minW: 6, minH: 2 },
    category: "metrics",
  },
  {
    id: "top-consumers",
    label: "Top Consumers",
    description: "VMs/containers using the most resources",
    defaultLayout: { w: 12, h: 5, minW: 6, minH: 3 },
    category: "metrics",
  },
];

export const defaultWidgetIds = [
  "stats-overview",
  "cluster-cards",
  "cpu-chart",
  "memory-chart",
  "disk-chart",
  "network-chart",
  "live-metrics",
  "top-consumers",
];

function makeLayoutItem(
  id: string,
  x: number,
  y: number,
  dl: WidgetDefinition["defaultLayout"],
): LayoutItem {
  const item: LayoutItem = { i: id, x, y, w: dl.w, h: dl.h };
  if (dl.minW != null) item.minW = dl.minW;
  if (dl.minH != null) item.minH = dl.minH;
  return item;
}

export function getDefaultLayout(): LayoutItem[] {
  let y = 0;
  const layouts: LayoutItem[] = [];

  for (const widgetId of defaultWidgetIds) {
    const def = widgetRegistry.find((w) => w.id === widgetId);
    if (!def) continue;

    const isHalfWidth = def.defaultLayout.w <= 6;
    const prevItem = layouts[layouts.length - 1];

    if (
      isHalfWidth &&
      prevItem !== undefined &&
      prevItem.w <= 6 &&
      prevItem.y === y
    ) {
      layouts.push(makeLayoutItem(widgetId, 6, y, def.defaultLayout));
      y += Math.max(def.defaultLayout.h, prevItem.h);
    } else {
      if (layouts.length > 0) {
        const prev = layouts[layouts.length - 1];
        if (prev !== undefined) {
          y = prev.y + prev.h;
        }
      }
      layouts.push(makeLayoutItem(widgetId, 0, y, def.defaultLayout));
      if (!isHalfWidth) {
        y += def.defaultLayout.h;
      }
    }
  }

  return layouts;
}

export interface DashboardPreset {
  name: string;
  widgetIds: string[];
  layouts: LayoutItem[];
}

export const defaultPreset: DashboardPreset = {
  name: "Default",
  widgetIds: defaultWidgetIds,
  layouts: getDefaultLayout(),
};
