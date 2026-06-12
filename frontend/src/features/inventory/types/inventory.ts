export type ResourceType = "vm" | "ct" | "node";

export type ResourceStatus =
  | "running"
  | "stopped"
  | "paused"
  | "suspended"
  | "online"
  | "offline"
  | "unknown";

export interface InventoryRow {
  /** Unique key: `${clusterId}:${type}:${id}` */
  key: string;
  id: string;
  type: ResourceType;
  name: string;
  status: ResourceStatus;
  clusterName: string;
  clusterId: string;
  nodeName: string;
  /** Proxmox VMID (VMs/CTs only) */
  vmid: number | null;
  cpuCount: number;
  memTotal: number;
  diskTotal: number;
  uptime: number;
  tags: string;
  haState: string;
  pool: string;
  template: boolean;
  /** Detected ostype: guest-agent OS ID when available, else Proxmox config setting */
  ostype: string;
  /** Proxmox config ostype (e.g. "l26", "win11"); used as a fallback for the icon when `ostype` is an unknown distro */
  configOstype: string;
  /** Live metric: CPU usage percent (null = no data) */
  cpuPercent: number | null;
  /** Live metric: memory usage percent (null = no data) */
  memPercent: number | null;
  /** Live metric: disk read bytes/sec (null = no data) */
  diskReadBps: number | null;
  /** Live metric: disk write bytes/sec (null = no data) */
  diskWriteBps: number | null;
  /** Live metric: network in bytes/sec (null = no data) */
  netInBps: number | null;
  /** Live metric: network out bytes/sec (null = no data) */
  netOutBps: number | null;
}

export type FilterOperator = "eq" | "neq" | "gt" | "lt";

export interface FilterCriteria {
  field: string;
  operator: FilterOperator;
  value: string;
}

export interface ParsedQuery {
  filters: FilterCriteria[];
  freeText: string;
}

export interface ColumnPreset {
  name: string;
  visibleColumns: string[];
}

export const DEFAULT_VISIBLE_COLUMNS = [
  "select",
  "type",
  "name",
  "status",
  "clusterName",
  "nodeName",
  "vmid",
  "cpuPercent",
  "memPercent",
  "network",
  "disk",
  "uptime",
] as const;

// Glanceable subset for phone widths (below md) — the Columns toggle can opt
// more back in; persisted separately from the desktop layout.
export const MOBILE_VISIBLE_COLUMNS = [
  "type",
  "name",
  "status",
  "cpuPercent",
  "memPercent",
] as const;

export const ALL_DATA_COLUMNS = [
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
] as const;
