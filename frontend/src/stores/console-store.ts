import { create } from "zustand";
import { persist } from "zustand/middleware";
import type {
  ConsoleTab,
  ConsoleStatus,
  ConsoleType,
} from "@/features/console/types/console";

interface ConsoleState {
  tabs: ConsoleTab[];
  activeTabId: string | null;
}

interface ConsoleActions {
  addTab: (tab: Omit<ConsoleTab, "id" | "status" | "reconnectKey">) => string;
  removeTab: (id: string) => void;
  setActiveTab: (id: string) => void;
  updateTabStatus: (id: string, status: ConsoleStatus) => void;
  reconnectTab: (id: string) => void;
  /** Update node for all tabs matching a VM and trigger reconnect. */
  updateTabNode: (clusterID: string, vmid: number, newNode: string) => void;
}

let nextId = Date.now();

function generateTabId(type: ConsoleType, node: string, vmid?: number): string {
  nextId++;
  const suffix = vmid !== undefined ? `-${String(vmid)}` : "";
  return `${type}-${node}${suffix}-${String(nextId)}`;
}

export const useConsoleStore = create<ConsoleState & ConsoleActions>()(
  persist(
    (set) => ({
      tabs: [],
      activeTabId: null,

      addTab: (tab) => {
        const id = generateTabId(tab.type, tab.node, tab.vmid);
        const newTab: ConsoleTab = { ...tab, id, status: "connecting", reconnectKey: 0 };
        set((state) => ({
          tabs: [...state.tabs, newTab],
          activeTabId: id,
        }));
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
          return { tabs: filtered, activeTabId: newActiveId };
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
    }),
    {
      name: "proxdash-console-tabs",
      // Only persist tab definitions and active tab, not transient connection status.
      partialize: (state) => ({
        tabs: state.tabs.map((t) => ({
          ...t,
          status: "connecting" as ConsoleStatus,
          reconnectKey: 0,
        })),
        activeTabId: state.activeTabId,
      }),
    },
  ),
);
