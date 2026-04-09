/**
 * Hand-written TypeScript types mirroring the subset of the Nexara backend
 * API that the mobile app calls. Verified against the Go handlers in
 * `internal/api/handlers/{clusters,nodes,vms,alerts,metrics}.go`.
 *
 * When the backend gains an OpenAPI spec we should replace this file with
 * generated types.
 */

// ── Auth ────────────────────────────────────────────────────────────────────

export interface AuthUser {
  id: string;
  email: string;
  display_name: string;
  role: string;
}

export interface AuthResponse {
  user: AuthUser;
  access_token: string;
  refresh_token: string;
  expires_at: number;
  permissions: string[];
}

export interface TOTPRequiredResponse {
  totp_required: true;
  totp_pending_token: string;
}

export type LoginResponse = AuthResponse | TOTPRequiredResponse;

export function isTOTPRequired(r: LoginResponse): r is TOTPRequiredResponse {
  return (r as TOTPRequiredResponse).totp_required === true;
}

// ── Console token (M1) ─────────────────────────────────────────────────────

export interface ConsoleTokenResponse {
  token: string;
  expires_in: number;
}

export type ConsoleType =
  | "node_shell"
  | "vm_serial"
  | "ct_attach"
  | "vm_vnc"
  | "ct_vnc";

export interface ConsoleTokenRequest {
  cluster_id: string;
  node: string;
  vmid?: number;
  type: ConsoleType;
}

// ── Snapshots ──────────────────────────────────────────────────────────────

/**
 * Mirrors `snapshotResponse` from `internal/api/handlers/vms.go:937-950`.
 *
 * The backend filters out the Proxmox "current" pseudo-snapshot (which
 * represents the live VM state) before returning the list, so we don't
 * have to deal with it client-side.
 *
 * `snap_time` is Unix seconds (int), not an ISO string. Convert via
 * `new Date(snap_time * 1000)` when formatting for display.
 *
 * `vmstate` is 0 or 1 — non-zero means the snapshot includes RAM state.
 * VMs (qemu) only; containers (lxc) always have vmstate=0 because LXC
 * snapshots are filesystem-only.
 */
export interface Snapshot {
  name: string;
  description?: string;
  snap_time?: number;
  vmstate?: number;
  parent?: string;
}

export interface CreateSnapshotRequest {
  snap_name: string;
  description?: string;
  vmstate?: boolean;
}

// ── Global search ──────────────────────────────────────────────────────────

export type SearchResultType = "vm" | "ct" | "node" | "storage" | "cluster";

/**
 * Mirrors `searchResult` from `internal/api/handlers/search.go:25-34`.
 *
 * - `node` and `status` are omitted on cluster-type results.
 * - `vmid` is only present for `vm` / `ct` types.
 * - `cluster_id` and `cluster_name` are always present (for cluster-type
 *   results they equal the cluster's own id/name).
 */
export interface SearchResult {
  type: SearchResultType;
  id: string;
  name: string;
  node?: string;
  status?: string;
  cluster_id: string;
  cluster_name: string;
  vmid?: number;
}

// ── Clusters ────────────────────────────────────────────────────────────────

export type ClusterStatus =
  | "online"
  | "degraded"
  | "offline"
  | "inactive"
  | "unknown";

export interface Cluster {
  id: string;
  name: string;
  api_url: string;
  token_id: string;
  tls_fingerprint: string;
  sync_interval_seconds: number;
  is_active: boolean;
  status: ClusterStatus;
  created_at: string;
  updated_at: string;
}

// ── Nodes ───────────────────────────────────────────────────────────────────

export type NodeStatus = "online" | "offline" | "unknown";

export interface Node {
  id: string;
  cluster_id: string;
  name: string;
  status: NodeStatus;
  cpu_count: number;
  cpu_model: string;
  cpu_cores: number;
  cpu_sockets: number;
  cpu_threads: number;
  cpu_mhz: string;
  mem_total: number;
  disk_total: number;
  swap_total: number;
  swap_used: number;
  swap_free: number;
  pve_version: string;
  kernel_version: string;
  dns_servers: string;
  dns_search: string;
  timezone: string;
  subscription_status: string;
  subscription_level: string;
  load_avg: string;
  io_wait: number;
  uptime: number;
  last_seen_at: string;
  created_at: string;
  updated_at: string;
}

// ── VMs and Containers ─────────────────────────────────────────────────────

export type VMStatus =
  | "running"
  | "stopped"
  | "paused"
  | "suspended"
  | "unknown";
export type VMType = "qemu" | "lxc";

export interface VM {
  id: string;
  cluster_id: string;
  node_id: string;
  vmid: number;
  name: string;
  type: VMType;
  status: VMStatus;
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

// ── Storage content ────────────────────────────────────────────────────────

/**
 * Mirrors `storageContentResponse` from `internal/api/handlers/storage.go:78-85`.
 *
 * Returned by `GET /api/v1/clusters/:cluster_id/storage/:storage_id/content`,
 * which proxies to Proxmox's `nodes/<node>/storage/<storage>/content` API.
 *
 * Field shapes:
 *
 *   - `volid` is the full Proxmox volume identifier, e.g.:
 *       "local:iso/debian-12.iso"
 *       "local:vztmpl/debian-12-standard_12.7-1_amd64.tar.zst"
 *       "local:backup/vzdump-qemu-100-2024_01_15-12_00_00.vma.zst"
 *       "local-lvm:vm-100-disk-0"
 *     The display filename is whatever follows the `:type/` prefix; see
 *     `extractVolumeName()` in the storage detail screen.
 *
 *   - `format` is the storage format, e.g. "iso", "raw", "qcow2", "subvol",
 *     "vmdk", "vma.zst" — useful for icons / labels but not always present.
 *
 *   - `size` is bytes; `ctime` is Unix seconds since epoch.
 *
 *   - `content` is the Proxmox content type the volume belongs to:
 *     "iso" / "vztmpl" / "backup" / "images" / "rootdir" / "snippets".
 *
 *   - `vmid` is only set for VM/CT-associated volumes (images, rootdir,
 *     and backups whose filename includes a vmid).
 */
export type StorageContentType =
  | "iso"
  | "vztmpl"
  | "backup"
  | "images"
  | "rootdir"
  | "snippets";

export interface StorageContentItem {
  volid: string;
  format: string;
  size: number;
  ctime: number;
  content: string;
  vmid?: number;
}

// ── Storage pools ──────────────────────────────────────────────────────────

/**
 * Mirrors `storageResponse` from `internal/api/handlers/storage.go:39-56`.
 *
 * Note: the backend returns one row per (node × storage name) tuple.
 * Shared storages (NFS, Ceph, etc.) with the same `storage` name show
 * up once per node in the cluster, each with its own row `id` but
 * typically identical capacity numbers. Non-shared storages (LVM,
 * local disk) have per-node rows with different capacities. The mobile
 * detail screen just renders whatever row the user tapped — no cross-
 * row dedup on the client side.
 *
 * `content` is a comma-separated list of Proxmox content types, e.g.
 * `"images,iso,vztmpl,backup"`. Split on comma for display.
 */
export interface StoragePool {
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

// ── Alerts ──────────────────────────────────────────────────────────────────

export type AlertSeverity = "critical" | "warning" | "info";
export type AlertState = "pending" | "firing" | "acknowledged" | "resolved";

export interface Alert {
  id: string;
  rule_id: string;
  state: AlertState;
  severity: AlertSeverity;
  cluster_id?: string;
  node_id?: string;
  vm_id?: string;
  resource_name: string;
  metric: string;
  current_value: number;
  threshold: number;
  message: string;
  escalation_level: number;
  channel_id?: string;
  pending_at: string;
  fired_at?: string;
  acknowledged_at?: string;
  acknowledged_by?: string;
  resolved_at?: string;
  resolved_by?: string;
  created_at: string;
}

export interface AlertSummary {
  firing_count: number;
  pending_count: number;
  acknowledged_count: number;
  critical_firing: number;
  warning_firing: number;
  info_firing: number;
}

export interface AlertListFilters {
  state?: AlertState;
  severity?: AlertSeverity;
  cluster_id?: string;
  limit?: number;
  offset?: number;
}

// ── Audit log ──────────────────────────────────────────────────────────────

export interface AuditLogEntry {
  id: string;
  cluster_id: string | null;
  user_id: string;
  resource_type: string;
  resource_id: string;
  action: string;
  details: string;
  created_at: string;
  source: string;
  user_email: string;
  user_display_name: string;
  cluster_name: string;
  resource_vmid: number;
  resource_name: string;
}

// ── Metric history ─────────────────────────────────────────────────────────

export type MetricRange = "1h" | "6h" | "24h" | "7d";

export interface MetricPoint {
  timestamp: number; // unix milliseconds
  cpuPercent: number;
  memPercent: number;
  diskReadBps: number;
  diskWriteBps: number;
  netInBps: number;
  netOutBps: number;
}

// ── Error envelope ──────────────────────────────────────────────────────────

export interface ApiError {
  error: string;
  message: string;
  details?: Record<string, unknown>;
}
