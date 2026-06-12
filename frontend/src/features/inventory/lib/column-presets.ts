import {
  DEFAULT_VISIBLE_COLUMNS,
  MOBILE_VISIBLE_COLUMNS,
} from "../types/inventory";

// Mobile and desktop persist independently: a curated phone layout must not
// overwrite the user's desktop column choices (and vice versa).
const STORAGE_KEY = "nexara:inventory:columns";
const STORAGE_KEY_MOBILE = "nexara:inventory:columns:mobile";

export function loadColumnVisibility(mobile = false): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(mobile ? STORAGE_KEY_MOBILE : STORAGE_KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null) return {};
    return parsed as Record<string, boolean>;
  } catch {
    return {};
  }
}

export function saveColumnVisibility(
  visibility: Record<string, boolean>,
  mobile = false,
): void {
  try {
    localStorage.setItem(
      mobile ? STORAGE_KEY_MOBILE : STORAGE_KEY,
      JSON.stringify(visibility),
    );
  } catch {
    // localStorage may be unavailable in some environments
  }
}

export function getDefaultColumnVisibility(
  mobile = false,
): Record<string, boolean> {
  const visibility: Record<string, boolean> = {};
  const defaults = new Set<string>(
    mobile ? MOBILE_VISIBLE_COLUMNS : DEFAULT_VISIBLE_COLUMNS,
  );

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
    "network",
    "disk",
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
