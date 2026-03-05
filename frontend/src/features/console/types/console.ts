export type ConsoleType = "node_shell" | "vm_serial" | "ct_attach" | "vm_vnc" | "ct_vnc";

export type ConsoleStatus = "connecting" | "connected" | "disconnected" | "error";

export interface ConsoleTab {
  id: string;
  clusterID: string;
  node: string;
  vmid?: number;
  type: ConsoleType;
  label: string;
  status: ConsoleStatus;
  reconnectKey: number;
}
