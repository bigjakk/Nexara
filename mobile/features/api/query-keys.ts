/**
 * Centralised TanStack Query keys for the mobile app. We mirror the keys
 * the web frontend uses (in `frontend/src/features/.../api/*-queries.ts`)
 * so that when we wire WebSocket-driven cache invalidation in M3, the
 * existing event handlers can be ported over near-verbatim.
 */

export const queryKeys = {
  all: ["nexara"] as const,

  // Clusters
  clusters: () => ["clusters"] as const,
  cluster: (id: string) => ["cluster", id] as const,

  // Nodes
  clusterNodes: (clusterId: string) =>
    ["clusters", clusterId, "nodes"] as const,

  // Storage pools (per-cluster list; single-pool view is a client-side
  // filter over the list, same pattern as useNode)
  clusterStorage: (clusterId: string) =>
    ["clusters", clusterId, "storage"] as const,

  // Storage content (per-pool ISOs / templates / backups / images list)
  storageContent: (clusterId: string, storageId: string) =>
    ["clusters", clusterId, "storage", storageId, "content"] as const,

  // VMs and containers
  clusterVMs: (clusterId: string) => ["clusters", clusterId, "vms"] as const,
  clusterContainers: (clusterId: string) =>
    ["clusters", clusterId, "containers"] as const,
  vm: (clusterId: string, vmId: string) =>
    ["clusters", clusterId, "vm", vmId] as const,

  // Alerts
  alerts: (filters?: Record<string, unknown>) =>
    filters ? (["alerts", filters] as const) : (["alerts"] as const),
  alertSummary: () => ["alert-summary"] as const,
  clusterAlerts: (clusterId: string) =>
    ["cluster-alerts", clusterId] as const,

  // Metrics
  metricHistory: (
    resourceType: "cluster" | "node" | "vm",
    resourceId: string,
    range: string,
  ) => ["metric-history", resourceType, resourceId, range] as const,

  // Global search
  search: (query: string) => ["search", query] as const,

  // Snapshots (per-VM/CT list)
  vmSnapshots: (clusterId: string, vmId: string) =>
    ["clusters", clusterId, "vm", vmId, "snapshots"] as const,
} as const;
