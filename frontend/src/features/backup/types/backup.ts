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

export interface RestoreRequest {
  pbs_server_id: string;
  backup_type: string;
  backup_id: string;
  backup_time: number;
  datastore: string;
  target_node: string;
  vmid: number;
  storage?: string;
}

export interface DeleteSnapshotRequest {
  backup_type: string;
  backup_id: string;
  backup_time: number;
}
