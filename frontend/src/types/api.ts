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
  permissions: string[];
}

// RBAC types
export interface RBACRole {
  id: string;
  name: string;
  description: string;
  is_builtin: boolean;
  permissions?: RBACPermission[];
  created_at: string;
  updated_at: string;
}

export interface RBACPermission {
  id: string;
  action: string;
  resource: string;
  description: string;
}

export interface RBACUserRole {
  id: string;
  user_id: string;
  role_id: string;
  role_name: string;
  role_description: string;
  is_builtin: boolean;
  scope_type: "global" | "cluster";
  scope_id?: string;
  created_at: string;
}

export interface UserListItem {
  id: string;
  email: string;
  display_name: string;
  role: string;
  roles: string[];
  is_active: boolean;
  auth_source: "local" | "ldap" | "oidc";
  totp_enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface LDAPConfig {
  id: string;
  name: string;
  enabled: boolean;
  server_url: string;
  start_tls: boolean;
  skip_tls_verify: boolean;
  bind_dn: string;
  bind_password_set: boolean;
  search_base_dn: string;
  user_filter: string;
  username_attribute: string;
  email_attribute: string;
  display_name_attribute: string;
  group_search_base_dn: string;
  group_filter: string;
  group_attribute: string;
  group_role_mapping: Record<string, string>;
  default_role_id: string | null;
  sync_interval_minutes: number;
  last_sync_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface LDAPConfigRequest {
  name: string;
  enabled: boolean;
  server_url: string;
  start_tls: boolean;
  skip_tls_verify: boolean;
  bind_dn: string;
  bind_password: string;
  search_base_dn: string;
  user_filter: string;
  username_attribute: string;
  email_attribute: string;
  display_name_attribute: string;
  group_search_base_dn: string;
  group_filter: string;
  group_attribute: string;
  group_role_mapping: Record<string, string>;
  default_role_id: string | null;
  sync_interval_minutes: number;
  /**
   * Confirms storing a config that carries passwords over a connection that is
   * unencrypted, or encrypted but unverified. Only sent after the operator
   * accepts the warning the backend returns on the first attempt.
   */
  acknowledge_insecure_tls?: boolean;
}

export interface LDAPTestResponse {
  success: boolean;
  message: string;
}

export interface LDAPSyncResponse {
  message: string;
  users_synced: number;
  users_disabled: number;
  users_re_enabled: number;
}

export interface OIDCConfig {
  id: string;
  name: string;
  enabled: boolean;
  issuer_url: string;
  client_id: string;
  client_secret_set: boolean;
  redirect_uri: string;
  scopes: string[];
  email_claim: string;
  display_name_claim: string;
  groups_claim: string;
  group_role_mapping: Record<string, string>;
  default_role_id: string | null;
  auto_provision: boolean;
  allowed_domains: string[];
  created_at: string;
  updated_at: string;
}

export interface OIDCConfigRequest {
  name: string;
  enabled: boolean;
  issuer_url: string;
  client_id: string;
  client_secret: string;
  redirect_uri: string;
  scopes: string[];
  email_claim: string;
  display_name_claim: string;
  groups_claim: string;
  group_role_mapping: Record<string, string>;
  default_role_id: string | null;
  auto_provision: boolean;
  allowed_domains: string[];
  /**
   * Confirms a plain-http callback on anything but loopback. The authorization
   * code rides back on that URL, so off loopback it crosses the network in the
   * clear. Only sent after the operator accepts the backend's refusal.
   */
  acknowledge_insecure_redirect?: boolean;
}

export interface OIDCTestResponse {
  success: boolean;
  message: string;
}

export interface OIDCAuthorizeResponse {
  redirect_url: string;
}

export interface SSOStatus {
  oidc_enabled: boolean;
  oidc_provider_name: string;
}

export interface MyPermissionsResponse {
  permissions: string[];
  roles: RBACUserRole[];
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

export interface TOTPSetupResponse {
  secret: string;
  otpauth_url: string;
}

export interface TOTPConfirmResponse {
  enabled: boolean;
  recovery_codes: string[];
}

export interface TOTPStatusResponse {
  enabled: boolean;
  recovery_codes_remaining: number;
}

/**
 * One of the caller's own active sessions, from GET /api/v1/auth/sessions.
 *
 * device_name and device_type are empty strings rather than null for sessions
 * created before the device columns existed — the server normalises them, so
 * the UI only has to handle "".
 */
export interface UserSession {
  id: string;
  device_name: string;
  device_type: string;
  user_agent: string;
  ip_address: string;
  created_at: string;
  last_used_at: string;
  expires_at: string;
  /** The session this request was made from; the UI warns before revoking it. */
  is_current: boolean;
}

/** A resource the caller has starred: cluster, node or guest. */
export type FavoriteResourceType = "cluster" | "node" | "vm";

/**
 * One starred resource, already resolved to what the sidebar needs to draw and
 * navigate to it without expanding its cluster.
 *
 * `ref` is the stable Proxmox identity the favorite is stored against (empty
 * for a cluster, the node name, the VMID) and is what an unstar sends back.
 * `target_id` is the row id to route to right now — the collector re-issues
 * those UUIDs, so it is resolved server-side on every read and must never be
 * cached as if it were the identity.
 */
export interface Favorite {
  resource_type: FavoriteResourceType;
  cluster_id: string;
  cluster_name: string;
  ref: string;
  target_id: string;
  name: string;
  status: string;
  /** "qemu" | "lxc" for a guest, "" otherwise. Picks the route and the icon. */
  vm_kind: string;
  vmid: number;
  /** The node a guest is on right now; empty for the other types. */
  node_name: string;
  template: boolean;
  ha_state: string;
  ostype: string;
  config_ostype: string;
  created_at: string;
}

export interface TOTPRequiredResponse {
  totp_required: boolean;
  totp_pending_token: string;
}

export interface TOTPVerifyLoginRequest {
  totp_pending_token: string;
  code?: string;
  recovery_code?: string;
}

// CVE Scanning types
export interface CVEScan {
  id: string;
  cluster_id: string;
  status: "pending" | "running" | "completed" | "failed";
  total_nodes: number;
  scanned_nodes: number;
  total_vulns: number;
  critical_count: number;
  high_count: number;
  medium_count: number;
  low_count: number;
  error_message?: string;
  started_at: string;
  completed_at?: string;
  created_at: string;
}

export interface CVEScanNode {
  id: string;
  scan_id: string;
  node_id: string;
  node_name: string;
  status: string;
  packages_total: number;
  vulns_found: number;
  posture_score: number;
  error_message?: string;
  scanned_at?: string;
}

export type SSVCLabel = "act" | "attend" | "track_star" | "track";

export interface CVEScanVuln {
  id: string;
  scan_id: string;
  scan_node_id: string;
  cve_id: string;
  package_name: string;
  current_version: string;
  fixed_version?: string;
  severity: "critical" | "high" | "medium" | "low" | "unknown";
  risk_severity: "critical" | "high" | "medium" | "low" | "unknown";
  risk_score: number;
  ssvc_label: SSVCLabel;
  cvss_score: number;
  epss?: number;
  epss_percentile?: number;
  kev: boolean;
  description: string;
}

export interface CVEScanDetail {
  scan: CVEScan;
  nodes: CVEScanNode[];
}

export interface SecurityPosture {
  scan_id: string;
  status: string;
  total_vulns: number;
  critical_count: number;
  high_count: number;
  medium_count: number;
  low_count: number;
  unknown_count: number;
  kev_count: number;
  act_count: number;
  attend_count: number;
  track_star_count: number;
  track_count: number;
  total_nodes: number;
  scanned_nodes: number;
  posture_score: number;
  started_at: string;
  completed_at?: string;
}

export interface CVEScanSchedule {
  cluster_id: string;
  enabled: boolean;
  interval_hours: number;
  updated_at?: string;
}

export interface CVENotifyConfig {
  cluster_id: string;
  enabled: boolean;
  notify_on_act: boolean;
  notify_on_attend: boolean;
  channel_ids: string[];
  cooldown_minutes: number;
  last_notified_at?: string;
}

// Alert types
export interface AlertRule {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  severity: "critical" | "warning" | "info";
  metric: string;
  operator: string;
  threshold: number;
  duration_seconds: number;
  /**
   * "global" is for metrics describing infrastructure no single cluster owns
   * — a Veeam repository holds every cluster's backups. A global rule has no
   * cluster_id, and the scoped alert-history read hides its alerts from
   * anyone but a holder of global view:alert.
   */
  scope_type: "cluster" | "node" | "vm" | "global";
  cluster_id?: string;
  node_id?: string;
  // Stable Proxmox VMID — vm-scoped rules key on (cluster_id, vm_vmid), not
  // the churn-prone vms-row UUID.
  vm_vmid?: number;
  cooldown_seconds: number;
  escalation_chain: EscalationStep[];
  message_template: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface EscalationStep {
  channel_id: string;
  delay_minutes: number;
}

export interface AlertInstance {
  id: string;
  rule_id: string;
  state: "pending" | "firing" | "acknowledged" | "resolved";
  severity: "critical" | "warning" | "info";
  cluster_id?: string;
  node_id?: string;
  vm_id?: string;
  /** Stable Proxmox guest VMID (alert_history.vm_vmid); absent for node/
   * cluster alerts and for pre-000077 rows whose vm_id had already churned. */
  vm_vmid?: number;
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

export type ChannelType =
  | "email"
  | "webhook"
  | "slack"
  | "discord"
  | "pagerduty"
  | "teams"
  | "telegram";

export interface NotificationChannel {
  id: string;
  name: string;
  channel_type: ChannelType;
  enabled: boolean;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface TestChannelResponse {
  success: boolean;
  message: string;
}

export type DLQState =
  "pending" | "rate_limited" | "retrying" | "resolved" | "dismissed";

export type DLQFailureKind = "send_failed" | "rate_limited" | "config_error";

export interface NotificationDLQEntry {
  id: string;
  channel_id?: string;
  channel_type: string;
  channel_name: string;
  alert_id?: string;
  rule_id?: string;
  payload: Record<string, unknown>;
  last_error: string;
  attempt_count: number;
  state: DLQState;
  failure_kind: DLQFailureKind;
  created_at: string;
  updated_at: string;
}

export interface NotificationDLQSummary {
  pending: number;
  rate_limited: number;
  retrying: number;
  resolved: number;
  dismissed: number;
}

export interface MaintenanceWindow {
  id: string;
  cluster_id: string;
  node_id?: string;
  description: string;
  starts_at: string;
  ends_at: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface AlertRuleRequest {
  name: string;
  description?: string | undefined;
  enabled?: boolean | undefined;
  severity?: "critical" | "warning" | "info" | undefined;
  metric: string;
  operator: string;
  threshold: number;
  duration_seconds?: number | undefined;
  scope_type?: "cluster" | "node" | "vm" | "global" | undefined;
  cluster_id?: string | undefined;
  node_id?: string | undefined;
  // Stable Proxmox VMID, matching the response type — the backend binds
  // vm_vmid and ignores anything else, so a vm_id here would be dropped.
  vm_vmid?: number | undefined;
  cooldown_seconds?: number | undefined;
  escalation_chain?: EscalationStep[] | undefined;
  message_template?: string | undefined;
}

// Report types
export type ReportType =
  | "cluster_digest"
  | "backup_compliance"
  | "resource_utilization"
  | "vm_resource_usage"
  | "capacity_forecast"
  | "snapshot_inventory"
  | "patch_status"
  | "uptime_summary";

/**
 * Per-report options, stored as JSON on schedules and runs. Every field has a
 * server-side default, so an empty object is the report as it has always
 * been; `sections` switches optional sections off by name.
 */
export interface ReportParameters {
  stale_after_hours?: number;
  top_n?: number;
  snapshot_warn_days?: number;
  sections?: Record<string, boolean>;
}

export interface ReportSchedule {
  id: string;
  name: string;
  report_type: ReportType;
  cluster_id: string;
  time_range_hours: number;
  schedule: string;
  format: "html" | "csv";
  email_enabled: boolean;
  email_channel_id?: string;
  email_recipients: string[];
  parameters: ReportParameters;
  enabled: boolean;
  last_run_at?: string;
  next_run_at?: string;
  created_by: string;
  /** Whose grants each run reads under: the last person to save the schedule. */
  run_as: string;
  created_at: string;
  updated_at: string;
}

export interface ReportRun {
  id: string;
  schedule_id?: string;
  report_type: ReportType;
  cluster_id: string;
  status: "pending" | "running" | "completed" | "failed";
  time_range_hours: number;
  parameters: ReportParameters;
  error_message?: string;
  created_by: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
}

export interface ApiError {
  error: string;
  message: string;
  details?: Record<string, unknown>;
}

/**
 * Asks Nexara to create the cluster's API credential itself instead of being
 * handed one. Mutually exclusive with token_id/token_secret.
 *
 * `password` and `otp` are spent on a single Proxmox login and never stored —
 * what Nexara keeps is the token it mints.
 */
export interface BootstrapClusterRequest {
  /** A privileged PVE account in name@realm form, e.g. root@pam. */
  username: string;
  password: string;
  /** Required only when the account has two-factor authentication enabled. */
  otp?: string;
  /** Defaults to nexara@pve. */
  user_id?: string;
  /** Defaults to nexara. */
  token_name?: string;
}

export interface CreateClusterRequest {
  name: string;
  api_url: string;
  /** Omitted when `bootstrap` is supplied. */
  token_id?: string;
  /** Omitted when `bootstrap` is supplied. */
  token_secret?: string;
  tls_fingerprint?: string;
  sync_interval_seconds?: number;
  allow_private_address?: boolean;
  bootstrap?: BootstrapClusterRequest;
}

/** One stage of what onboarding did on the Proxmox side. */
export interface BootstrapStep {
  step: "user" | "acl" | "token" | "verify";
  status: "created" | "existed" | "verified";
  detail?: string;
}

/** Names only — the minted secret never crosses the wire. */
export interface BootstrapSummary {
  token_id: string;
  steps: BootstrapStep[];
}

export interface ConnectivityResult {
  reachable: boolean;
  message: string;
}

export interface CreateClusterResponse {
  cluster: ClusterResponse;
  connectivity: ConnectivityResult;
  /** Present only when the cluster was onboarded with a `bootstrap` block. */
  bootstrap?: BootstrapSummary;
}

export interface UpdateClusterResponse {
  cluster: ClusterResponse;
  connectivity: ConnectivityResult;
}

/** A single Ceph health check (the reason behind a HEALTH_WARN/HEALTH_ERR). */
export interface CephHealthCheckItem {
  type: string;
  severity: string;
  message: string;
  /** Per-daemon/per-resource specifics shown under the summary (e.g. which OSDs). */
  detail: string[];
}

export type HealthSeverity = "err" | "warn";

/** One infrastructure-health problem attached to a cluster (server-computed). */
export interface HealthIssue {
  type: string;
  severity: HealthSeverity;
  scope: "cluster" | "node" | "storage" | "guest";
  /** Affected resource name; "" for cluster-scoped issues. */
  target: string;
  summary: string;
  detail: string;
}

export interface ClusterResponse {
  id: string;
  name: string;
  api_url: string;
  token_id: string;
  tls_fingerprint: string;
  sync_interval_seconds: number;
  is_active: boolean;
  status: "online" | "degraded" | "offline" | "inactive" | "unknown";
  pve_version: string;
  created_at: string;
  updated_at: string;
  /**
   * Where the stored API credential came from. Deleting the cluster can only
   * offer to revoke it on the Proxmox side when Nexara minted it.
   */
  credential_source: "manual" | "bootstrap";
  /** Current health problems (Ceph, HA, disks, storage, failed tasks, …); absent when healthy. */
  issues?: HealthIssue[];
}

export interface NodeResponse {
  id: string;
  cluster_id: string;
  name: string;
  address: string;
  status: string;
  ha_state: string;
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

export interface NodeDiskResponse {
  id: string;
  dev_path: string;
  model: string;
  serial: string;
  size: number;
  disk_type: string;
  health: string;
  wearout: string;
  rpm: number;
  vendor: string;
  wwn: string;
}

export interface NodeNetworkInterfaceResponse {
  id: string;
  iface: string;
  iface_type: string;
  active: boolean;
  autostart: boolean;
  method: string;
  method6: string;
  address: string;
  netmask: string;
  gateway: string;
  cidr: string;
  bridge_ports: string;
  comments: string;
  /** 0 when the interface does not configure an MTU explicitly. */
  mtu: number;
}

export interface NodePCIDeviceResponse {
  id: string;
  pci_id: string;
  class: string;
  device_name: string;
  vendor_name: string;
  device: string;
  vendor: string;
  iommu_group: number;
  subsystem_device: string;
  subsystem_vendor: string;
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
  ostype: string;
  config_ostype: string;
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

// Rolling Update types
export interface RollingUpdateJob {
  id: string;
  cluster_id: string;
  status:
    "pending" | "running" | "paused" | "completed" | "failed" | "cancelled";
  parallelism: number;
  reboot_after_update: boolean;
  auto_restore_guests: boolean;
  package_excludes: string[];
  ha_policy: "strict" | "warn";
  ha_warnings: HAConflict[] | null;
  auto_upgrade: boolean;
  /** False means the job upgrades each node in place, leaving its guests
   * running. Lets the progress view explain why a node shows no drain. */
  drain_guests: boolean;
  failure_reason: string;
  notify_channel_id?: string;
  created_by: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
}

export interface RollingUpdateNode {
  id: string;
  job_id: string;
  node_name: string;
  node_order: number;
  step:
    | "pending"
    | "draining"
    | "awaiting_upgrade"
    | "upgrading"
    | "rebooting"
    | "health_check"
    | "restoring"
    | "completed"
    | "failed"
    | "skipped";
  failure_reason: string;
  skip_reason?: string;
  /** Upgrade applied, but the node still owes a reboot it could not take
   * because guests were running on it. Only set on an in-place job. */
  reboot_required?: boolean;
  packages_json: AptPackage[];
  guests_json: GuestSnapshot[];
  drain_started_at?: string;
  drain_completed_at?: string;
  upgrade_confirmed_at?: string;
  upgrade_started_at?: string;
  upgrade_completed_at?: string;
  upgrade_output?: string;
  reboot_started_at?: string;
  reboot_completed_at?: string;
  health_check_at?: string;
  restore_started_at?: string;
  restore_completed_at?: string;
  created_at: string;
  updated_at: string;
}

export interface AptPackage {
  Package: string;
  Title: string;
  Description: string;
  OldVersion: string;
  Version: string;
  Origin: string;
  Priority: string;
  Section: string;
  ChangeLogUrl: string;
}

export interface GuestSnapshot {
  vmid: number;
  name: string;
  type: "qemu" | "lxc";
  status: string;
  passthrough?: boolean;
}

export interface CreateRollingUpdateRequest {
  nodes: string[];
  parallelism: number;
  reboot_after_update: boolean;
  auto_restore_guests: boolean;
  package_excludes: string[];
  ha_policy: "strict" | "warn";
  auto_upgrade: boolean;
  notify_channel_id?: string | undefined;
  /**
   * False upgrades each node in place, leaving its guests running.
   *
   * Omitted means true, matching the server default and the only behaviour
   * that existed before: a job that drains each node before touching it.
   */
  drain_guests?: boolean | undefined;
}

export interface SSHCredential {
  cluster_id: string;
  username: string;
  port: number;
  auth_type: "password" | "key";
  has_key: boolean;
  created_at: string;
  updated_at: string;
}

export interface SSHHostKeyPending {
  host: string;
  port: number;
  fingerprint: string;
  public_key: string;
}

export interface SSHHostKeyMismatch {
  host: string;
  port: number;
  expected_fingerprint: string;
  presented_fingerprint: string;
  presented_public_key: string;
}

export interface SSHTestResponse {
  success: boolean;
  message: string;
  fingerprint?: string;
  host_key_pending?: SSHHostKeyPending;
  host_key_mismatch?: SSHHostKeyMismatch;
}

export interface SSHKnownHost {
  id: string;
  cluster_id: string;
  host: string;
  port: number;
  fingerprint: string;
  pinned_by?: string;
  pinned_at: string;
}

export interface HAConflict {
  source: string;
  rule_name: string;
  type: string;
  severity: "error" | "warning";
  vmid: number;
  vm_name?: string;
  message: string;
  node: string;
}

export interface HAPreFlightReport {
  conflicts: HAConflict[];
  has_errors: boolean;
}

// API Key types
export interface APIKeyResponse {
  id: string;
  name: string;
  key_prefix: string;
  expires_at: string | null;
  last_used_at: string | null;
  last_used_ip: string | null;
  is_revoked: boolean;
  created_at: string;
}

export interface CreateAPIKeyRequest {
  name: string;
  expires_in?: number | undefined;
}

export interface CreateAPIKeyResponse {
  id: string;
  name: string;
  key: string;
  key_prefix: string;
  expires_at: string | null;
  created_at: string;
}

// APT Repository types from Proxmox GET /nodes/{node}/apt/repositories
export interface AptRepositoryResponse {
  digest: string;
  files: AptRepositoryFile[] | null;
  infos: AptRepositoryInfo[] | null;
  "standard-repos": AptStandardRepo[] | null;
  errors: AptRepositoryError[] | null;
}

export interface AptRepositoryFile {
  path: string;
  "file-type": string;
  repositories: AptRepository[];
}

export interface AptRepository {
  Types: string[] | null;
  URIs: string[] | null;
  Suites: string[] | null;
  Components: string[] | null;
  Options: AptRepoOption[] | null;
  Comment: string;
  Enabled: number;
  FileType: string;
}

export interface AptRepoOption {
  Key: string;
  Values: string[];
}

export interface AptRepositoryInfo {
  path: string;
  index: number;
  property: string;
  kind: string;
  message: string;
}

export interface AptStandardRepo {
  handle: string;
  name: string;
  status: number;
  description: string;
}

export interface AptRepositoryError {
  path: string;
  error: string;
}

/**
 * One parameter of an endpoint the server declares a schema for.
 *
 * `optional` and `default` are two separate facts and the UI must keep them
 * apart. "Optional, no default" means the endpoint does something else when
 * the caller stays silent — `index` on the disk-attach endpoint takes the
 * lowest FREE slot — while "optional, default 0" would mean it behaves as if
 * slot 0 had been asked for, which on a VM with a disk is its boot disk.
 * Collapsing the two is how an external consumer destroyed one.
 *
 * The server omits `default` entirely when there is none, so the absence of
 * the key is the signal — not a null, and not a zero value. A `false` or `0`
 * default IS sent and must render.
 */
export interface APIParameter {
  name: string;
  type: string;
  /** Where the value goes on the wire. */
  source: "path" | "query" | "body";
  optional: boolean;
  /** Present only when the parameter declares one. */
  default?: unknown;
  enum?: string[];
  /** Named validation+normalisation rule, e.g. "uuid", "disk-size". */
  format?: string;
  /**
   * What the rule named by `format` — or spelled out by `pattern` —
   * actually permits. Absent when neither names a catalogued rule.
   */
  rule?: APIRule;
  /** Human-readable value shape, e.g. "<number><K|M|G|T|P>". */
  typetext?: string;
  description?: string;
  /** Parameters the caller must send alongside this one. */
  requires?: string[];
  /** Regex a string value must match. */
  pattern?: string;
  /**
   * Bounds. Absent means "no bound" — and a bound of 0 is a real bound, so
   * every read must test for `undefined` rather than for falsiness. `if
   * (p.minimum)` silently drops a floor of 0, which is the same
   * absent-versus-zero trap as `default`.
   */
  minimum?: number;
  maximum?: number;
  min_length?: number;
  max_length?: number;
  /**
   * A second name the endpoint also accepts for this parameter. Omitting it
   * from the table documents the endpoint as rejecting input it accepts.
   */
  alias?: string;
  /** Element schema when `type` is "array". */
  items?: APIItems;
}

/**
 * The rule behind a parameter's `format` or `pattern`.
 *
 * A NAME is not a RULE: `format: "pve-configid"` says a rule applies and
 * not what it is, which leaves a caller to go and read the server's source.
 * This is the server's own one-line statement of what the rule permits,
 * plus the regex where the rule IS one regex.
 */
export interface APIRule {
  /** The catalogued rule's name, e.g. "pve-configid", "pve-object-id". */
  name: string;
  /** One line saying what the rule allows. */
  permits: string;
  /**
   * The rule's regular expression — present ONLY when the regex is the
   * whole check. A rule that validates by parsing (`ip`, `disk-size`)
   * publishes none, and `permits` is then the whole statement. Absence is
   * the signal: compile this when it is here, read `permits` when it is
   * not.
   */
  regex?: string;
}

/**
 * An array parameter's element schema.
 *
 * Deliberately narrower than APIParameter: the server's schema engine allows
 * only scalar element types and forbids an element from being optional,
 * carrying a default, naming a source, declaring an alias or requiring a
 * companion — so there are no such fields to render, and an element can never
 * itself be an array.
 */
export interface APIItems {
  type: string;
  enum?: string[];
  format?: string;
  pattern?: string;
  /** The element's own rule, on the same terms as APIParameter.rule. */
  rule?: APIRule;
  typetext?: string;
  description?: string;
  minimum?: number;
  maximum?: number;
  min_length?: number;
  max_length?: number;
}

export interface APIEndpoint {
  method: string;
  path: string;
  description: string;
  permission: string;
  group: string;
  /**
   * Absent whenever the server has no parameter list to send: a declared
   * route that declares no parameters (and so refuses any query key, and on
   * a POST, PUT or PATCH any key in a JSON body; an upload's multipart fields
   * are its handler's to read), one of the few routes that predate the
   * declaration layer and publish no schema, or an older server that
   * predates the field — which is why it is optional rather than an empty
   * array. The payload does not tell those cases apart, so an absent list
   * does not mark a route as legacy.
   */
  parameters?: APIParameter[];
}

// VM folder organisation (used by the "VMs & Templates" tree perspective).
export interface VMFolder {
  id: string;
  cluster_id: string;
  parent_id: string | null;
  name: string;
}

export interface VMFolderMembership {
  vm_id: string;
  folder_id: string;
}

export interface VMFolderListResponse {
  folders: VMFolder[];
  memberships: VMFolderMembership[];
}

// --- VM Import ---

export interface ImportWarning {
  type: string;
  key?: string;
  value?: string;
}

export interface ImportDisk {
  volid: string;
  size?: number;
}

export interface ImportMetadataResponse {
  type: string;
  source: string;
  name: string;
  cores: number;
  sockets: number;
  memory: number;
  ostype: string;
  create_args: Record<string, string>;
  disks: Record<string, ImportDisk>;
  warnings: ImportWarning[];
}

export interface ImportSourceContentItem {
  volid: string;
  format: string;
  size: number;
  ctime: number;
  content: string;
  vmid?: number;
}

// A deduplicated import-capable storage (or ESXi source) as seen at the cluster level,
// paired with an online node from which it can be browsed. Shared storages appear once;
// non-shared per-node storages appear once per node.
export interface ImportSource {
  storage: string;
  type: string;
  content: string;
  shared: boolean;
  node: string;
  pool_id?: string;
}

export interface ImportSourceContent {
  node: string;
  storage: string;
  items: ImportSourceContentItem[];
}

// Result of the query-url-metadata probe (Proxmox-detected filename/size for a remote URL).
export interface URLMetadataResponse {
  filename?: string;
  size?: number;
  mimetype?: string;
}

export interface VMImportJob {
  id: string;
  cluster_id: string;
  source_acquisition: string;
  source_format: string;
  source_ref: string;
  target_node: string;
  target_storage: string;
  target_vmid: number;
  name: string;
  status: string;
  upid?: string;
  failure_reason?: string;
  warnings: ImportWarning[];
  options: Record<string, unknown>;
  created_by: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
}

export interface StartImportRequest {
  node: string;
  storage: string;
  volume: string;
  source_format: string;
  source_acquisition: string;
  target_node: string;
  target_storage: string;
  working_storage?: string;
  bridge?: string;
  vmid?: number;
  name?: string;
  disk_format?: string;
  start_after?: boolean;
  live_import?: boolean;

  // Guest-config overrides (omit to keep the source-derived value).
  cores?: number;
  sockets?: number;
  memory?: number;
  cpu_type?: string;
  os_type?: string;
  bios?: string;
  machine?: string;
  scsihw?: string;
  pool?: string;
  tags?: string;
  description?: string;
  onboot?: boolean;
  agent?: boolean;
  numa?: boolean;

  // Network options for the synthesised NIC (only applied when bridge is set).
  net_model?: string;
  vlan_tag?: number;
  firewall?: boolean;
  mac_address?: string;
  rate_limit?: string;
  mtu?: number;
  multiqueue?: number;
}

export interface EsxiSourceRequest {
  storage: string;
  server: string;
  username: string;
  password: string;
  skip_cert_verification: boolean;
  nodes?: string;
}
