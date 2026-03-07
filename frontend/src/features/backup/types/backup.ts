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

export interface PBSDatastoreConfig {
  name: string;
  path?: string;
  comment?: string;
  "gc-schedule"?: string;
  "prune-schedule"?: string;
  "keep-last"?: number;
  "keep-daily"?: number;
  "keep-weekly"?: number;
  "keep-monthly"?: number;
  "keep-yearly"?: number;
  "notify-user"?: string;
  notify?: string;
  "verify-new"?: boolean;
  "maintenance-mode"?: string;
}

export interface BackupCoverageEntry {
  vmid: number;
  name: string;
  type: string;
  status: string;
  cluster_id: string;
  cluster_name: string;
  latest_backup: number | null;
  backup_count: number;
  coverage_status: "recent" | "stale" | "none";
}

export interface BackupJobParams {
  enabled?: number;
  type?: string;
  schedule?: string;
  storage?: string;
  node?: string;
  vmid?: string;
  mode?: string;
  compress?: string;
  mailnotification?: string;
  mailto?: string;
  comment?: string;
}
