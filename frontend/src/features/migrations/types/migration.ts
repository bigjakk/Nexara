export type MigrationStatus =
  | "pending"
  | "checking"
  | "migrating"
  | "completed"
  | "failed"
  | "cancelled";

export type MigrationType = "intra-cluster" | "cross-cluster";

export type MigrationMode = "live" | "storage" | "both";

export type VMType = "qemu" | "lxc";

export type CheckSeverity = "pass" | "warn" | "fail";

export interface CheckResult {
  name: string;
  severity: CheckSeverity;
  message: string;
}

export interface PreFlightReport {
  checks: CheckResult[];
  passed: boolean;
}

export interface MigrationJob {
  id: string;
  source_cluster_id: string;
  target_cluster_id: string;
  source_node: string;
  target_node: string;
  vmid: number;
  vm_type: VMType;
  migration_type: MigrationType;
  migration_mode: MigrationMode;
  storage_map: Record<string, string>;
  network_map: Record<string, string>;
  online: boolean;
  bwlimit_kib: number;
  delete_source: boolean;
  target_vmid: number;
  target_storage: string;
  status: MigrationStatus;
  upid: string;
  progress: number;
  check_results: PreFlightReport | null;
  error_message: string;
  started_at: string | null;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface CreateMigrationRequest {
  source_cluster_id: string;
  target_cluster_id: string;
  source_node: string;
  target_node: string;
  vmid: number;
  vm_type: VMType;
  migration_type: MigrationType;
  migration_mode: MigrationMode;
  storage_map: Record<string, string>;
  network_map: Record<string, string>;
  online: boolean;
  bwlimit_kib: number;
  delete_source: boolean;
  target_vmid: number;
  target_storage: string;
}
