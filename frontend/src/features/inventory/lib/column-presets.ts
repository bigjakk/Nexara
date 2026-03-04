import { DEFAULT_VISIBLE_COLUMNS } from "../types/inventory";

const STORAGE_KEY = "proxdash:inventory:columns";

export function loadColumnVisibility(): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null) return {};
    return parsed as Record<string, boolean>;
  } catch {
    return {};
  }
}

export function saveColumnVisibility(visibility: Record<string, boolean>): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(visibility));
  } catch {
    // localStorage may be unavailable in some environments
  }
}

export function getDefaultColumnVisibility(): Record<string, boolean> {
  const visibility: Record<string, boolean> = {};
  const defaults = new Set<string>(DEFAULT_VISIBLE_COLUMNS);

  // Hide columns not in defaults
  const allColumns = [
    "select",
    "type",
    "name",
    "status",
    "clusterName",
    "nodeName",
    "vmid",
    "cpuCount",
    "memTotal",
    "diskTotal",
    "cpuPercent",
    "memPercent",
    "uptime",
    "tags",
    "haState",
    "pool",
    "template",
  ];

  for (const col of allColumns) {
    if (!defaults.has(col)) {
      visibility[col] = false;
    }
  }

  return visibility;
}
