export type DRSMode = "disabled" | "advisory" | "automatic";

export type RuleType = "affinity" | "anti-affinity" | "pin";

export interface DRSWeights {
  cpu: number;
  memory: number;
}

export interface DRSConfig {
  id: string;
  cluster_id: string;
  mode: DRSMode;
  enabled: boolean;
  weights: DRSWeights;
  imbalance_threshold: number;
  eval_interval_seconds: number;
  created_at: string;
  updated_at: string;
}

export interface DRSConfigRequest {
  mode: DRSMode;
  enabled: boolean;
  weights: DRSWeights;
  imbalance_threshold: number;
  eval_interval_seconds: number;
}

export type RuleSource = "manual" | "ha";

export interface DRSRule {
  id: string;
  cluster_id: string;
  rule_type: RuleType;
  vm_ids: number[];
  node_names: string[];
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
