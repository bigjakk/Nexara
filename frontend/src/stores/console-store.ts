import { create } from "zustand";
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
  addTab: (tab: Omit<ConsoleTab, "id" | "status">) => string;
  removeTab: (id: string) => void;
  setActiveTab: (id: string) => void;
  updateTabStatus: (id: string, status: ConsoleStatus) => void;
}

let nextId = 0;

function generateTabId(type: ConsoleType, node: string, vmid?: number): string {
  nextId++;
  const suffix = vmid !== undefined ? `-${String(vmid)}` : "";
  return `${type}-${node}${suffix}-${String(nextId)}`;
}

export const useConsoleStore = create<ConsoleState & ConsoleActions>()(
  (set) => ({
    tabs: [],
    activeTabId: null,

    addTab: (tab) => {
      const id = generateTabId(tab.type, tab.node, tab.vmid);
      const newTab: ConsoleTab = { ...tab, id, status: "connecting" };
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
  }),
);
