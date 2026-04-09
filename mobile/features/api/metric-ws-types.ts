/**
 * Live metric WebSocket payload types — mirrors the JSON shape published
 * by `internal/collector/publisher.go::PublishClusterMetrics()` to the
 * `nexara:metrics:<cluster_id>` Redis channel (which the WS server
 * republishes as `cluster:<cluster_id>:metrics` to clients).
 *
 * The payload contains COMPLETE cluster state in every message:
 *   - All nodes with their current CPU usage (0-1 float) and cumulative
 *     mem/disk/net counters
 *   - All VMs with the same shape
 *
 * Rate calculation (BPS for disk/net) is the client's responsibility:
 * keep the previous snapshot's cumulative counters per cluster, diff
 * on each new message, divide by elapsed seconds.
 *
 * Field naming follows the backend's `json:"snake_case"` tags exactly,
 * NOT camelCase like the rest of the mobile types — these are the raw
 * shapes coming over the wire.
 */

export interface NodeMetricSnapshot {
  node_id: string;
  cpu_usage: number;
  mem_used: number;
  mem_total: number;
  disk_read: number;
  disk_write: number;
  net_in: number;
  net_out: number;
}

export interface VMMetricSnapshot {
  vm_id: string;
  cpu_usage: number;
  mem_used: number;
  mem_total: number;
  disk_read: number;
  disk_write: number;
  net_in: number;
  net_out: number;
}

export interface ClusterMetricSummary {
  cluster_id: string;
  collected_at: string;
  node_count: number;
  vm_count: number;
  nodes: NodeMetricSnapshot[];
  vms: VMMetricSnapshot[];
}
