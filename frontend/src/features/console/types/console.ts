export type ConsoleType = "node_shell" | "vm_serial" | "ct_attach" | "vm_vnc" | "ct_vnc";

export type ConsoleStatus = "connecting" | "connected" | "disconnected" | "error";

export type ConsoleResourceKind = "vm" | "ct";

export interface ConsoleTab {
  id: string;
  clusterID: string;
  node: string;
  vmid?: number;
  type: ConsoleType;
  label: string;
  status: ConsoleStatus;
  reconnectKey: number;
  /** DB UUID of the VM/CT resource (for lifecycle actions). */
  resourceId?: string;
  /** Whether this is a VM or container. */
  kind?: ConsoleResourceKind;
}
