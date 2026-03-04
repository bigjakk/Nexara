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
  /** Live metric: CPU usage percent (null = no data) */
  cpuPercent: number | null;
  /** Live metric: memory usage percent (null = no data) */
  memPercent: number | null;
}

export type FilterOperator = "eq" | "gt" | "lt";

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
  "uptime",
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
  "uptime",
  "tags",
  "haState",
  "pool",
  "template",
] as const;
