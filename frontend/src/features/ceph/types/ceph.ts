export interface CephStatus {
  health: CephHealth;
  pgmap: CephPGMap;
  osdmap: CephOSDMap;
  monmap: CephMonMap;
}

export interface CephHealth {
  status: string;
}

export interface CephPGMap {
  bytes_used: number;
  bytes_avail: number;
  bytes_total: number;
  read_bytes_sec: number;
  write_bytes_sec: number;
  read_op_per_sec: number;
  write_op_per_sec: number;
  num_pgs: number;
}

export interface CephOSDMap {
  num_osds: number;
  num_up_osds: number;
  num_in_osds: number;
  full: boolean;
  nearfull: boolean;
}

export interface CephMonMap {
  num_mons: number;
}

export interface CephOSD {
  id: number;
  name: string;
  host: string;
  up: number;
  in: number;
  status: string;
  crush_weight: number;
}

export interface CephPool {
  pool_name: string;
  pool: number;
  size: number;
  min_size: number;
  pg_num: number;
  pg_autoscale_mode: string;
  crush_rule: number;
  bytes_used: number;
  percent_used: number;
  read_bytes_sec: number;
  write_bytes_sec: number;
  read_op_per_sec: number;
  write_op_per_sec: number;
}

export interface CephMon {
  name: string;
  addr: string;
  host: string;
  rank: number;
}

export interface CephFS {
  name: string;
  metadata_pool: string;
  data_pool: string;
}

export interface CephCrushRule {
  rule_id: number;
  rule_name: string;
  type: number;
  min_size: number;
  max_size: number;
}

export interface CreatePoolRequest {
  name: string;
  size: number;
  min_size?: number;
  pg_num: number;
  application?: string;
  crush_rule_name?: string;
  pg_autoscale_mode?: string;
}

export interface CephClusterMetric {
  time: string;
  cluster_id: string;
  health_status: string;
  osds_total: number;
  osds_up: number;
  osds_in: number;
  pgs_total: number;
  bytes_used: number;
  bytes_avail: number;
  bytes_total: number;
  read_ops_sec: number;
  write_ops_sec: number;
  read_bytes_sec: number;
  write_bytes_sec: number;
}

export interface CephOSDMetric {
  time: string;
  cluster_id: string;
  osd_id: number;
  osd_name: string;
  host: string;
  status_up: boolean;
  status_in: boolean;
  crush_weight: number;
}

export interface CephPoolMetric {
  time: string;
  cluster_id: string;
  pool_id: number;
  pool_name: string;
  size: number;
  min_size: number;
  pg_num: number;
  bytes_used: number;
  percent_used: number;
  read_ops_sec: number;
  write_ops_sec: number;
  read_bytes_sec: number;
  write_bytes_sec: number;
}
