import type { ConsoleTab } from "./types/console";

/**
 * Shared console tab fixtures.
 *
 * Test-only, and deliberately DATA ONLY — type imports and nothing else. The
 * socket stand-in and the store installer live in `console.mocks.ts`, so a
 * test that only needs a tab shape does not pull in `vitest` or the console
 * store to get one.
 */

/** A node shell tab — the Terminal's subject. */
export function shellTab(over: Partial<ConsoleTab> = {}): ConsoleTab {
  return {
    id: "shell-tab-1",
    clusterID: "cluster-1",
    node: "node1",
    type: "node_shell",
    label: "node1 shell",
    status: "connecting",
    reconnectKey: 0,
    ...over,
  };
}

/** A VM VNC tab — the VNCViewer's subject. */
export function vncTab(over: Partial<ConsoleTab> = {}): ConsoleTab {
  return {
    id: "vnc-tab-1",
    clusterID: "cluster-1",
    node: "pve1",
    type: "vm_vnc",
    vmid: 103,
    label: "VNC: zorin",
    status: "connecting",
    reconnectKey: 0,
    ...over,
  };
}
