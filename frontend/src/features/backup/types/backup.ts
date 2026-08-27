export interface PBSServer {
  id: string;
  name: string;
  api_url: string;
  token_id: string;
  tls_fingerprint: string;
  cluster_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface PBSDatastore {
  name: string;
  path?: string;
  comment?: string;
}

export interface PBSDatastoreStatus {
  store: string;
  total: number;
  used: number;
  avail: number;
}

export interface PBSSnapshot {
  id: string;
  pbs_server_id: string;
  datastore: string;
  backup_type: string;
  backup_id: string;
  backup_time: number;
  size: number;
  verified: boolean;
  protected: boolean;
  comment: string;
  owner: string;
  last_seen_at: string;
  created_at: string;
  updated_at: string;
}

export interface PBSSyncJob {
  id: string;
  pbs_server_id: string;
  job_id: string;
  store: string;
  remote: string;
  remote_store: string;
  schedule: string;
  last_run_state: string;
  next_run: number;
  comment: string;
  last_seen_at: string;
  created_at: string;
  updated_at: string;
}

export interface PBSVerifyJob {
  id: string;
  pbs_server_id: string;
  job_id: string;
  store: string;
  schedule: string;
  last_run_state: string;
  comment: string;
  last_seen_at: string;
  created_at: string;
  updated_at: string;
}

export interface PBSTask {
  upid: string;
  node: string;
  pid: number;
  starttime: number;
  endtime?: number;
  status?: string;
  worker_type: string;
  user: string;
}

export interface PBSTaskStatus {
  upid: string;
  status: string;
  exitstatus?: string;
  worker_type: string;
  starttime: number;
  endtime?: number;
}

export interface PBSDatastoreMetric {
  time: string;
  pbs_server_id: string;
  datastore: string;
  total: number;
  used: number;
  avail: number;
}

export interface PBSDatastoreRRDEntry {
  time: number;
  total?: number | null;
  used?: number | null;
  available?: number | null;
  read_bytes?: number | null;
  write_bytes?: number | null;
  read_ios?: number | null;
  write_ios?: number | null;
  io_ticks?: number | null;
}

export interface RestoreRequest {
  pbs_server_id: string;
  backup_type: string;
  backup_id: string;
  backup_time: number;
  datastore: string;
  target_node: string;
  vmid: number;
  storage?: string;
  force?: boolean;
  unique?: boolean;
  start_after_restore?: boolean;
}

export interface DeleteSnapshotRequest {
  backup_type: string;
  backup_id: string;
  backup_time: number;
}

export interface ProtectSnapshotRequest {
  backup_type: string;
  backup_id: string;
  backup_time: number;
  protected: boolean;
}

export interface UpdateSnapshotNotesRequest {
  backup_type: string;
  backup_id: string;
  backup_time: number;
  comment: string;
}

export interface PBSTaskLogEntry {
  n: number;
  t: string;
}

export interface PBSPruneRequest {
  backup_type?: string;
  backup_id?: string;
  dry_run: boolean;
  keep_last: number;
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
  keep_yearly: number;
}

export interface PBSPruneResult {
  "backup-type": string;
  "backup-id": string;
  "backup-time": number;
  keep: boolean;
  protected?: boolean;
}

export interface BackupJob {
  id: string;
  enabled?: number;
  type: string;
  schedule?: string;
  storage?: string;
  node?: string;
  vmid?: string;
  /** 1 when the job backs up every guest (PVE's "all" selection mode). */
  all?: number;
  /** VMIDs skipped by an all-guests job. */
  exclude?: string;
  /** Resource pool whose members the job backs up. */
  pool?: string;
  mode?: string;
  compress?: string;
  mailnotification?: string;
  mailto?: string;
  "next-run"?: number;
  comment?: string;
}

export interface TriggerBackupRequest {
  vmid: string;
  storage?: string;
  mode?: string;
  compress?: string;
  node: string;
}

/**
 * A prune job from GET /api/v1/pbs-servers/:id/prune-jobs.
 *
 * Prune is NOT part of a datastore's own config on any supported PBS — 2.2
 * moved it into datastore-independent jobs — so a datastore can prune daily
 * while its config reports no prune-schedule at all.
 */
export interface PBSPruneJob {
  id: string;
  store: string;
  schedule?: string;
  comment?: string;
  /** PBS's own spelling: the job is disabled when true. */
  disable?: boolean;
  ns?: string;
  "max-depth"?: number;
  "keep-last"?: number;
  "keep-hourly"?: number;
  "keep-daily"?: number;
  "keep-weekly"?: number;
  "keep-monthly"?: number;
  "keep-yearly"?: number;
  "last-run-state"?: string;
  /** Unix seconds; absent when the job has never run. */
  "last-run-endtime"?: number;
  "last-run-upid"?: string;
  "next-run"?: number;
}

export interface PBSDatastoreConfig {
  name: string;
  path?: string;
  comment?: string;
  "gc-schedule"?: string;
  "prune-schedule"?: string;
  "keep-last"?: number;
  "keep-hourly"?: number;
  "keep-daily"?: number;
  "keep-weekly"?: number;
  "keep-monthly"?: number;
  "keep-yearly"?: number;
  "notify-user"?: string;
  notify?: string;
  "verify-new"?: boolean;
  "maintenance-mode"?: string;
  /** PBS 3.x replacement for the notify/notify-user pair above. */
  "notification-mode"?: string;
}

/** Which providers actually protect a guest. */
export type BackupProtection = "both" | "veeam" | "pbs" | "none" | "not_eligible";

/**
 * Why a guest is not a backup target at all. Templates never appear in the
 * coverage report, so they are not represented here.
 */
export type BackupEligibility = "eligible" | "veeam_worker" | "veeam_backup_server";

/** The Veeam side of one guest's protection. Absent when no Veeam data applies. */
export interface VeeamCoverage {
  protected: boolean;
  latest_restore_point: string | null;
  restore_point_count: number;
  restore_point_bytes: number;
  /**
   * How the guest was tied to its Veeam backup. "name" is a guess and must be
   * flagged as such — a rebuilt host reuses its name.
   */
  match_method: "smbios" | "name" | "manual" | "none";
  /** Verdict on the NEWEST restore point only. */
  malware_status: string;
  last_run_failed: boolean;
}

export interface BackupCoverageEntry {
  vmid: number;
  name: string;
  type: string;
  status: string;
  cluster_id: string;
  cluster_name: string;
  /** PBS figures. A Veeam-only guest has null/0 here and is still protected. */
  latest_backup: number | null;
  backup_count: number;
  /** Freshness across every provider the caller can see. */
  coverage_status: "recent" | "stale" | "none" | "not_eligible";
  eligibility: BackupEligibility;
  /** False for LXC containers, which Veeam cannot back up. PBS still can. */
  veeam_capable: boolean;
  veeam: VeeamCoverage | null;
  protection: BackupProtection;
}

export interface BackupJobParams {
  enabled?: number;
  type?: string;
  schedule?: string;
  storage?: string;
  node?: string;
  vmid?: string;
  all?: number;
  exclude?: string;
  pool?: string;
  mode?: string;
  compress?: string;
  mailnotification?: string;
  mailto?: string;
  comment?: string;
}

// --- Veeam Backup & Replication ---

export interface VeeamServer {
  id: string;
  name: string;
  base_url: string;
  username: string;
  /** Negotiated x-api-version, e.g. "1.3-rev2". */
  api_revision: string;
  /** VBR build, e.g. "13.1.0.411". */
  product_version: string;
  license_edition: string;
  tls_fingerprint: string;
  verify_tls: boolean;
  enabled: boolean;
  last_sync_at: string | null;
  last_sync_error: string;
  created_at: string;
  updated_at: string;
}

/** One Proxmox connection as Veeam's licence reports it. */
export interface VeeamProxmoxCluster {
  name: string;
  vm_count: number;
}

/**
 * Result of a connection test. Carries no credential, so it is safe to render
 * in full. `warnings` are conditions worth showing that do not stop the server
 * being registered — a non-Enterprise-Plus licence, or no Proxmox workloads.
 */
export interface VeeamProbeResult {
  api_revision: string;
  server_name: string;
  build_version: string;
  platform: string;
  license_edition: string;
  license_status: string;
  license_type: string;
  licensed_to: string;
  license_expiration: string;
  proxmox_clusters: VeeamProxmoxCluster[];
  warnings: string[];
}

export interface CreateVeeamServerRequest {
  name: string;
  base_url: string;
  username: string;
  password: string;
  tls_fingerprint?: string;
  verify_tls?: boolean;
  allow_private_address?: boolean;
}

export interface UpdateVeeamServerRequest {
  name?: string;
  base_url?: string;
  username?: string;
  password?: string;
  tls_fingerprint?: string;
  verify_tls?: boolean;
  enabled?: boolean;
  allow_private_address?: boolean;
}

export interface VeeamRepository {
  id: string;
  veeam_id: string;
  name: string;
  type: string;
  host_name: string;
  path: string;
  capacity_bytes: number;
  free_bytes: number;
  used_bytes: number;
  is_online: boolean;
  is_out_of_date: boolean;
  last_seen_at: string;
}

export interface VeeamRepositoryMetric {
  time: string;
  capacity_bytes: number;
  free_bytes: number;
  used_bytes: number;
}

export interface VeeamJob {
  id: string;
  veeam_id: string;
  name: string;
  job_type: string;
  workload: string;
  description: string;
  status: string;
  last_result: string;
  last_run: string | null;
  next_run: string | null;
  next_run_policy: string;
  repository_name: string;
  objects_count: number;
  progress_percent: number;
  /** Veeam's own bottleneck analysis: Source/Target/Network/Proxy/NotDefined. */
  bottleneck: string;
  /** Pre-formatted by Veeam ("00:18:27"); there is no machine-readable form. */
  duration: string;
  processing_rate: string;
  processed_size: number;
  read_size: number;
  transferred_size: number;
  /** Null until an operator maps this job's Veeam platform to a cluster. */
  cluster_id: string | null;
  last_seen_at: string;
}

export interface VeeamSession {
  id: string;
  veeam_id: string;
  name: string;
  state: string;
  result: string;
  result_message: string;
  algorithm: string;
  bottleneck: string;
  duration: string;
  processing_rate: string;
  processed_size: number;
  read_size: number;
  transferred_size: number;
  progress_percent: number;
  creation_time: string;
  end_time: string | null;
  initiated_by: string;
  /** True when Nexara itself started or stopped the run. */
  nexara_initiated: boolean;
  cluster_id: string | null;
}

export interface VeeamBackupObject {
  id: string;
  veeam_object_id: string;
  /** Veeam's objectId, which for Proxmox is the guest's smbios1 uuid. */
  smbios_uuid: string;
  name: string;
  object_type: string;
  restore_points_count: number;
  size_bytes: number;
  last_run_failed: boolean;
  cluster_id: string | null;
  last_seen_at: string;
}

/**
 * A Veeam "platform" is one Proxmox connection — a platformId that every
 * backup object, restore point and session from that cluster carries. The
 * mapping to a Nexara cluster is operator-confirmed and load-bearing for
 * authorization: until it exists, that platform's rows are visible only to a
 * holder of global view:veeam.
 */
export interface VeeamPlatform {
  platform_id: string;
  /** From the licence workload list, e.g. "CRJLAB". Often empty. */
  display_name: string;
  cluster_id: string | null;
  cluster_name: string;
  object_count: number;
  last_seen_at: string;
}

/** A guest belonging to the Veeam deployment rather than to the workload. */
export interface VeeamInfrastructureGuest {
  id: string;
  veeam_ref: string;
  role: "worker" | "backup_server";
  name: string;
  /** The Proxmox node a worker was deployed to, or "This server". */
  host_name: string;
  is_disabled: boolean;
  /** Workers are powered off between runs; false is the healthy state. */
  is_online: boolean;
  cluster_id: string | null;
  cluster_name: string;
  vmid: number | null;
  guest_name: string;
  last_seen_at: string;
}

/**
 * A backup object whose platform IS mapped to a cluster but which matches no
 * guest on it — a deleted VM, a template whose name was reused under a new
 * uuid, a host rebuilt in place. Restore points held for a machine that no
 * longer exists in the form that was backed up.
 */
export interface VeeamOrphanedObject {
  id: string;
  veeam_object_id: string;
  smbios_uuid: string;
  name: string;
  object_type: string;
  platform_name: string;
  cluster_id: string | null;
  cluster_name: string;
  restore_points_count: number;
  restore_point_bytes: number;
  latest_restore_point: string | null;
  size_bytes: number;
  last_run_failed: boolean;
  last_seen_at: string;
}

/** One guest's Veeam protection, for the VM detail card. */
export interface VeeamGuestProtection {
  protected: boolean;
  latest_restore_point: string | null;
  malware_status: string;
  object_count: number;
  restore_point_count: number;
  restore_point_bytes: number;
  match_method: "smbios" | "name" | "manual" | "none";
  last_run_failed: boolean;
  restore_points: VeeamGuestRestorePoint[];
}

export interface VeeamGuestRestorePoint {
  id: string;
  veeam_id: string;
  name: string;
  point_type: string;
  malware_status: string;
  guest_os_family: string;
  creation_time: string;
  size_bytes: number;
  supports_flr: boolean;
  /** Which backup this point belongs to — a guest appears in several. */
  object_name: string;
}

export interface VeeamRestorePoint {
  id: string;
  veeam_id: string;
  name: string;
  point_type: string;
  malware_status: string;
  guest_os_family: string;
  creation_time: string;
  size_bytes: number;
  supports_flr: boolean;
}
