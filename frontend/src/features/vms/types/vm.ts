export type VMAction =
  | "start"
  | "stop"
  | "shutdown"
  | "reboot"
  | "reset"
  | "suspend"
  | "resume";

export interface VMActionRequest {
  action: VMAction;
}

export interface VMActionResponse {
  upid: string;
  status: string;
}

export interface CloneRequest {
  new_id: number;
  name: string;
  target: string;
  full: boolean;
  storage: string;
}

export interface MigrateRequest {
  target: string;
  online: boolean;
}

export interface TaskStatusResponse {
  status: string;
  exit_status: string;
  type: string;
  upid: string;
  node: string;
  pid: number;
  start_time: number;
  progress?: number;
}

export type ResourceKind = "vm" | "ct";
