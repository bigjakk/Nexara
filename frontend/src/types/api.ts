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

export interface CVEScanVuln {
  id: string;
  scan_id: string;
  scan_node_id: string;
  cve_id: string;
  package_name: string;
  current_version: string;
  fixed_version?: string;
  severity: "critical" | "high" | "medium" | "low" | "unknown";
  cvss_score: number;
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
  scope_type: "cluster" | "node" | "vm";
  cluster_id?: string;
  node_id?: string;
  vm_id?: string;
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
  scope_type?: "cluster" | "node" | "vm" | undefined;
  cluster_id?: string | undefined;
  node_id?: string | undefined;
  vm_id?: string | undefined;
  cooldown_seconds?: number | undefined;
  escalation_chain?: EscalationStep[] | undefined;
  message_template?: string | undefined;
}

// Report types
export type ReportType =
  | "resource_utilization"
  | "capacity_forecast"
  | "backup_compliance"
  | "patch_status"
  | "uptime_summary";

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
  parameters: Record<string, unknown>;
  enabled: boolean;
  last_run_at?: string;
  next_run_at?: string;
  created_by: string;
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

// Rolling Update types
export interface RollingUpdateJob {
  id: string;
  cluster_id: string;
  status:
    | "pending"
    | "running"
    | "paused"
    | "completed"
    | "failed"
    | "cancelled";
  parallelism: number;
  reboot_after_update: boolean;
  auto_restore_guests: boolean;
  package_excludes: string[];
  ha_policy: "strict" | "warn";
  ha_warnings: HAConflict[];
  auto_upgrade: boolean;
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

export interface SSHTestResponse {
  success: boolean;
  message: string;
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
