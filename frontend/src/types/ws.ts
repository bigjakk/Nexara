// Client -> Server message types
export type WsClientMessageType = "subscribe" | "unsubscribe" | "ping";

// Server -> Client message types
export type WsServerMessageType =
  | "welcome"
  | "subscribed"
  | "data"
  | "error"
  | "pong";

/** Message sent from the client to the WebSocket server. */
export interface WsOutgoingMessage {
  type: WsClientMessageType;
  channels?: string[];
}

/** Message received from the WebSocket server. */
export interface WsIncomingMessage {
  type: WsServerMessageType;
  channel?: string;
  message?: string;
  payload?: unknown;
}

// --- Metric payload types (mirrors internal/collector/publisher.go) ---

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

export interface VmMetricSnapshot {
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
  vms: VmMetricSnapshot[];
}

// --- Frontend-only metric types ---

export type WsConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "reconnecting";

/** A single aggregated data point for time-series charts. */
export interface MetricDataPoint {
  timestamp: number;
  cpuPercent: number;
  memPercent: number;
  diskReadBps: number;
  diskWriteBps: number;
  netInBps: number;
  netOutBps: number;
}

/** Per-VM live metric snapshot (CPU and memory percent). */
export interface VmLiveMetric {
  cpuPercent: number;
  memPercent: number;
}

/** Aggregated live metrics for a single cluster. */
export interface AggregatedMetrics {
  cpuPercent: number;
  memPercent: number;
  memUsed: number;
  memTotal: number;
  diskReadBps: number;
  diskWriteBps: number;
  netInBps: number;
  netOutBps: number;
  nodeCount: number;
  vmCount: number;
  healthScore: number;
  history: MetricDataPoint[];
  topConsumers: TopConsumer[];
  /** Live metrics for ALL VMs in the cluster, keyed by VM UUID. */
  vmMetrics: Map<string, VmLiveMetric>;
  /** Live metrics for ALL nodes in the cluster, keyed by node UUID. */
  nodeMetrics: Map<string, VmLiveMetric>;
}

/** A VM ranked by resource consumption. */
export interface TopConsumer {
  vmId: string;
  cpuPercent: number;
  memPercent: number;
  memUsed: number;
  memTotal: number;
}
