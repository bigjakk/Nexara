import type { ClusterResponse, NodeResponse } from "@/types/api";

// A cluster and a node as the API returns them, with overrides.
export function makeCluster(
  overrides: Partial<ClusterResponse> = {},
): ClusterResponse {
  return {
    id: "c1",
    name: "test-cluster",
    api_url: "https://pve:8006",
    token_id: "user@pam!token",
    tls_fingerprint: "",
    sync_interval_seconds: 30,
    is_active: true,
    status: "online",
    pve_version: "",
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    credential_source: "manual",
    ...overrides,
  };
}

export function makeNode(overrides: Partial<NodeResponse> = {}): NodeResponse {
  return {
    id: "n1",
    cluster_id: "c1",
    name: "pve-node-1",
    address: "",
    status: "online",
    ha_state: "",
    cpu_count: 16,
    cpu_model: "Intel Xeon E5-2680 v4",
    cpu_cores: 14,
    cpu_sockets: 2,
    cpu_threads: 2,
    cpu_mhz: "2400",
    mem_total: 68719476736,
    disk_total: 500000000000,
    swap_total: 8589934592,
    swap_used: 0,
    swap_free: 8589934592,
    pve_version: "8.2.4",
    kernel_version: "6.8.12-1-pve",
    dns_servers: "8.8.8.8, 1.1.1.1",
    dns_search: "example.com",
    timezone: "UTC",
    subscription_status: "active",
    subscription_level: "community",
    load_avg: "0.50, 0.30, 0.20",
    io_wait: 0.02,
    uptime: 86400,
    last_seen_at: "2024-01-01T00:00:00Z",
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    ...overrides,
  };
}
