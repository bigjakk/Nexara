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

export interface Snapshot {
  name: string;
  description?: string;
  snap_time?: number;
  vmstate?: number;
  parent?: string;
}

export interface SnapshotRequest {
  snap_name: string;
  description?: string;
  vmstate?: boolean;
}

export interface CreateVMRequest {
  vmid: number;
  name: string;
  node: string;
  memory: number;
  cores: number;
  sockets: number;
  scsi0?: string;
  ide2?: string;
  net0: string;
  ostype: string;
  boot: string;
  cdrom?: string;
  start: boolean;
  // System
  bios?: string;
  machine?: string;
  scsihw?: string;
  efidisk0?: string;
  tpmstate0?: string;
  agent?: string;
  // CPU
  cpu?: string;
  numa?: boolean;
  // Memory
  balloon?: number;
  // Display
  vga?: string;
  // Boot / Options
  onboot?: boolean;
  hotplug?: string;
  tablet?: boolean;
  // Cloud-Init
  ciuser?: string;
  cipassword?: string;
  sshkeys?: string;
  ipconfig0?: string;
  nameserver?: string;
  searchdomain?: string;
  // Meta
  description?: string;
  tags?: string;
  pool?: string;
  // Extra fields forwarded to Proxmox (additional disks, CD-ROMs, etc.)
  extra?: Record<string, string>;
}

export interface CreateCTRequest {
  vmid: number;
  hostname: string;
  node: string;
  ostemplate: string;
  storage: string;
  rootfs: string;
  memory: number;
  swap: number;
  cores: number;
  net0: string;
  password: string;
  ssh_keys: string;
  unprivileged: boolean;
  start: boolean;
  description?: string | undefined;
  tags?: string | undefined;
  pool?: string | undefined;
  nameserver?: string | undefined;
  searchdomain?: string | undefined;
  extra?: Record<string, string> | undefined;
}

export type VMConfig = Record<string, unknown>;
