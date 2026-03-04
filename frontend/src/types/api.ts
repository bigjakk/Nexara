export interface User {
  id: string;
  email: string;
  display_name: string;
  role: "admin" | "user";
}

export interface AuthResponse {
  user: User;
  access_token: string;
  refresh_token: string;
  expires_at: number;
}

export interface LoginRequest {
  email: string;
  password: string;
}

export interface RegisterRequest {
  email: string;
  password: string;
  display_name: string;
}

export interface RefreshRequest {
  refresh_token: string;
}

export interface LogoutRequest {
  refresh_token: string;
}

export interface SetupStatus {
  needs_setup: boolean;
}

export interface ApiError {
  error: string;
  message: string;
  details?: Record<string, unknown>;
}

export interface CreateClusterRequest {
  name: string;
  api_url: string;
  token_id: string;
  token_secret: string;
  tls_fingerprint?: string;
  sync_interval_seconds?: number;
}

export interface ConnectivityResult {
  reachable: boolean;
  message: string;
}

export interface CreateClusterResponse {
  cluster: ClusterResponse;
  connectivity: ConnectivityResult;
}

export interface ClusterResponse {
  id: string;
  name: string;
  api_url: string;
  token_id: string;
  tls_fingerprint: string;
  sync_interval_seconds: number;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export interface NodeResponse {
  id: string;
  cluster_id: string;
  name: string;
  status: string;
  cpu_count: number;
  mem_total: number;
  disk_total: number;
  pve_version: string;
  uptime: number;
  last_seen_at: string;
  created_at: string;
  updated_at: string;
}

export interface VMResponse {
  id: string;
  cluster_id: string;
  node_id: string;
  vmid: number;
  name: string;
  type: string;
  status: string;
  cpu_count: number;
  mem_total: number;
  disk_total: number;
  uptime: number;
  template: boolean;
  tags: string;
  ha_state: string;
  pool: string;
  last_seen_at: string;
  created_at: string;
  updated_at: string;
}

export type TimeRange = "live" | "1h" | "6h" | "24h" | "7d";

export interface HistoricalMetricPoint {
  timestamp: number;
  cpuPercent: number;
  memPercent: number;
  diskReadBps: number;
  diskWriteBps: number;
  netInBps: number;
  netOutBps: number;
}

export interface StorageResponse {
  id: string;
  cluster_id: string;
  node_id: string;
  storage: string;
  type: string;
  content: string;
  active: boolean;
  enabled: boolean;
  shared: boolean;
  total: number;
  used: number;
  avail: number;
  last_seen_at: string;
  created_at: string;
  updated_at: string;
}
