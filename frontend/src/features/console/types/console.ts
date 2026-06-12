export type ConsoleType = "node_shell" | "vm_serial" | "ct_attach" | "vm_vnc" | "ct_vnc";

/**
 * Auto-reconnect budget for transient console drops. Live migrations need
 * the longest window: the guest keeps running but its node changes, and
 * resolveAndReconnect only finds the new node once the collector has synced
 * it. Stopped guests don't burn retries — they park as "guest-stopped".
 */
export const MAX_CONSOLE_AUTO_RETRIES = 5;

export type ConsoleStatus =
  | "connecting"
  | "connected"
  | "disconnected"
  | "reconnecting"
  | "error"
  /** Guest is powered off — parked until it powers back on (no auto-retries). */
  | "guest-stopped";

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
