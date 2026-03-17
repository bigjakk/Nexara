import { create } from "zustand";
import { persist } from "zustand/middleware";
import { apiClient } from "@/lib/api-client";
import type {
  ConsoleTab,
  ConsoleStatus,
  ConsoleType,
} from "@/features/console/types/console";

export type WindowMode = "hidden" | "minimized" | "floating" | "maximized";

interface ConsoleState {
  tabs: ConsoleTab[];
  activeTabId: string | null;
  windowMode: WindowMode;
  windowPosition: { x: number; y: number };
  windowSize: { width: number; height: number };
}

interface ConsoleActions {
  addTab: (tab: Omit<ConsoleTab, "id" | "status" | "reconnectKey">) => string;
  removeTab: (id: string) => void;
  setActiveTab: (id: string) => void;
  updateTabStatus: (id: string, status: ConsoleStatus) => void;
  reconnectTab: (id: string) => void;
  /** Update node for all tabs matching a VM and trigger reconnect. */
  updateTabNode: (clusterID: string, vmid: number, newNode: string) => void;
  /** Resolve the VM's current node via API and reconnect. */
  resolveAndReconnect: (id: string) => Promise<void>;
  setWindowMode: (mode: WindowMode) => void;
  setWindowPosition: (pos: { x: number; y: number }) => void;
  setWindowSize: (size: { width: number; height: number }) => void;
  /** Show the floating console — if hidden opens as floating, if minimized restores. */
  showConsole: () => void;
}

let nextId = Date.now();

function generateTabId(type: ConsoleType, node: string, vmid?: number): string {
  nextId++;
  const suffix = vmid !== undefined ? `-${String(vmid)}` : "";
  return `${type}-${node}${suffix}-${String(nextId)}`;
}

function defaultPosition(): { x: number; y: number } {
  if (typeof window === "undefined") return { x: 100, y: 100 };
  return { x: Math.max(0, window.innerWidth - 820), y: Math.max(0, window.innerHeight - 520) };
}

export const useConsoleStore = create<ConsoleState & ConsoleActions>()(
  persist(
    (set, get) => ({
      tabs: [],
      activeTabId: null,
      windowMode: "hidden" as WindowMode,
      windowPosition: defaultPosition(),
      windowSize: { width: 800, height: 500 },

      addTab: (tab) => {
        const state = get();

        // If a tab already exists for the same console target, focus it instead of duplicating
        const existing = state.tabs.find(
          (t) =>
            t.clusterID === tab.clusterID &&
            t.node === tab.node &&
            t.type === tab.type &&
            t.vmid === tab.vmid,
        );
        if (existing) {
          const newWindowMode = state.windowMode === "hidden" ? "floating" as WindowMode : state.windowMode;
          set({ activeTabId: existing.id, windowMode: newWindowMode });
          return existing.id;
        }

        const id = generateTabId(tab.type, tab.node, tab.vmid);
        const newTab: ConsoleTab = { ...tab, id, status: "connecting", reconnectKey: 0 };
        const newWindowMode = state.windowMode === "hidden" ? "floating" as WindowMode : state.windowMode;
        set({
          tabs: [...state.tabs, newTab],
          activeTabId: id,
          windowMode: newWindowMode,
        });
        return id;
      },

      removeTab: (id) => {
        set((state) => {
          const filtered = state.tabs.filter((t) => t.id !== id);
          let newActiveId = state.activeTabId;
          if (state.activeTabId === id) {
            const lastTab = filtered[filtered.length - 1];
            newActiveId = lastTab !== undefined ? lastTab.id : null;
          }
          const newWindowMode = filtered.length === 0 ? "hidden" as WindowMode : state.windowMode;
          return { tabs: filtered, activeTabId: newActiveId, windowMode: newWindowMode };
        });
      },

      setActiveTab: (id) => {
        set({ activeTabId: id });
      },

      updateTabStatus: (id, status) => {
        set((state) => ({
          tabs: state.tabs.map((t) => (t.id === id ? { ...t, status } : t)),
        }));
      },

      reconnectTab: (id) => {
        set((state) => ({
          tabs: state.tabs.map((t) =>
            t.id === id
              ? { ...t, status: "connecting" as ConsoleStatus, reconnectKey: t.reconnectKey + 1 }
              : t,
          ),
        }));
      },

      updateTabNode: (clusterID, vmid, newNode) => {
        set((state) => ({
          tabs: state.tabs.map((t) =>
            t.clusterID === clusterID && t.vmid === vmid && t.node !== newNode
              ? { ...t, node: newNode, status: "connecting" as ConsoleStatus, reconnectKey: t.reconnectKey + 1 }
              : t,
          ),
        }));
      },

      resolveAndReconnect: async (id) => {
        const state = get();
        const tab = state.tabs.find((t) => t.id === id);
        if (!tab) return;

        // For VM/CT tabs with a resourceId, resolve the current node before reconnecting.
        // This handles DRS migrations where the VM has moved to a different node.
        if (tab.vmid !== undefined && tab.resourceId && tab.kind) {
          try {
            const vmEndpoint =
              tab.kind === "ct"
                ? `/api/v1/clusters/${tab.clusterID}/containers/${tab.resourceId}`
                : `/api/v1/clusters/${tab.clusterID}/vms/${tab.resourceId}`;
            const vm = await apiClient.get<{ node_id: string }>(vmEndpoint);

            const nodesEndpoint = `/api/v1/clusters/${tab.clusterID}/nodes`;
            const nodes = await apiClient.get<Array<{ id: string; name: string }>>(nodesEndpoint);
            const resolved = nodes.find((n) => n.id === vm.node_id);

            if (resolved && resolved.name !== tab.node) {
              // Node changed (migration) — update node and reconnect
              set((s) => ({
                tabs: s.tabs.map((t) =>
                  t.id === id
                    ? { ...t, node: resolved.name, status: "connecting" as ConsoleStatus, reconnectKey: t.reconnectKey + 1 }
                    : t,
                ),
              }));
              return;
            }
          } catch {
            // Failed to resolve — reconnect with current node
          }
        }

        // Same node or node_shell — just reconnect
        set((s) => ({
          tabs: s.tabs.map((t) =>
            t.id === id
              ? { ...t, status: "connecting" as ConsoleStatus, reconnectKey: t.reconnectKey + 1 }
              : t,
          ),
        }));
      },

      setWindowMode: (mode) => {
        set({ windowMode: mode });
      },

      setWindowPosition: (pos) => {
        set({ windowPosition: pos });
      },

      setWindowSize: (size) => {
        set({ windowSize: { width: Math.max(400, size.width), height: Math.max(300, size.height) } });
      },

      showConsole: () => {
        const state = get();
        if (state.windowMode === "hidden" || state.windowMode === "minimized") {
          set({ windowMode: "floating" });
        }
      },
    }),
    {
      name: "nexara-console-tabs",
      partialize: (state) => ({
        tabs: state.tabs.map((t) => ({
          ...t,
          status: "connecting" as ConsoleStatus,
          reconnectKey: 0,
        })),
        activeTabId: state.activeTabId,
        windowMode: state.tabs.length > 0 ? state.windowMode : "hidden" as WindowMode,
        windowPosition: state.windowPosition,
        windowSize: state.windowSize,
      }),
    },
  ),
);
