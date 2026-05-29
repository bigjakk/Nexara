export type DRSMode = "disabled" | "advisory" | "automatic";

export type RuleType = "affinity" | "anti-affinity" | "pin";

export interface DRSWeights {
  cpu: number;
  memory: number;
}

export interface NativeCRSStatus {
  ha: string;
  auto_rebalance: boolean;
  threshold: number;
  hold_duration: number;
  margin: number;
  method: string;
  rebalance_on_start: boolean;
}

export interface DRSConfig {
  id: string;
  cluster_id: string;
  mode: DRSMode;
  enabled: boolean;
  weights: DRSWeights;
  imbalance_threshold: number;
  eval_interval_seconds: number;
  include_containers: boolean;
  created_at: string;
  updated_at: string;
  /** Proxmox native CRS state (PVE 9.2+); present when the cluster has CRS configured. */
  native_crs?: NativeCRSStatus;
}

export interface DRSConfigRequest {
  mode: DRSMode;
  weights: DRSWeights;
  imbalance_threshold: number;
  eval_interval_seconds: number;
  include_containers: boolean;
}

export type RuleSource = "manual" | "ha";

export interface DRSRule {
  id: string;
  cluster_id: string;
  rule_type: RuleType;
  vm_ids: number[] | null;
  node_names: string[] | null;
  enabled: boolean;
  source: RuleSource;
  ha_rule_name?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateRuleRequest {
  rule_type: RuleType;
  vm_ids: number[];
  node_names: string[];
  enabled: boolean;
}

export interface CreateHARuleRequest {
  rule_name: string;
  rule_type: RuleType;
  vm_ids: number[];
  node_names: string[];
  enabled: boolean;
}

export interface DRSRecommendation {
  vmid: number;
  vm_type: string;
  from: string;
  to: string;
  reason: string;
  improvement: number;
}

export interface NodeScore {
  node: string;
  score: number;
  cpu_load: number;
  mem_load: number;
}

export interface EvaluateResponse {
  recommendations: DRSRecommendation[];
  count: number;
  node_scores: NodeScore[] | null;
  imbalance: number;
  threshold: number;
  /** True when DRS automatic migrations were suppressed by Proxmox native CRS. */
  blocked?: boolean;
  block_reason?: string;
}

export interface DRSHistoryEntry {
  id: string;
  cluster_id: string;
  source_node: string;
  target_node: string;
  vm_id: number;
  vm_type: string;
  reason: string;
  score_before: number;
  score_after: number;
  status: string;
  executed_at: string | null;
  created_at: string;
}
