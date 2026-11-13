// The windows a datastore's charts can look back over. Usage is read from the
// samples Nexara stores; I/O comes from the Proxmox Backup Server's own RRD,
// which keeps different windows. Plain values, kept out of the component
// files so those export components only.

export const USAGE_TIMEFRAMES = ["1h", "6h", "24h", "7d"] as const;
export type UsageTimeframe = (typeof USAGE_TIMEFRAMES)[number];

export const IO_TIMEFRAMES = [
  { value: "hour", label: "1h" },
  { value: "day", label: "24h" },
  { value: "week", label: "7d" },
  { value: "month", label: "30d" },
] as const;
export type IOTimeframe = (typeof IO_TIMEFRAMES)[number]["value"];
