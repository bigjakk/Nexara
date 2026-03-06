import { create } from "zustand";

interface SidebarState {
  collapsed: boolean;
  expandedNodes: Set<string>;
}

interface SidebarActions {
  toggleCollapsed: () => void;
  toggleNode: (key: string) => void;
  expandNode: (key: string) => void;
}

const STORAGE_KEY = "proxdash-sidebar";

function loadPersistedState(): SidebarState {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed: unknown = JSON.parse(raw);
      if (parsed && typeof parsed === "object") {
        const obj = parsed as Record<string, unknown>;
        return {
          collapsed: typeof obj["collapsed"] === "boolean" ? obj["collapsed"] : false,
          expandedNodes: Array.isArray(obj["expandedNodes"])
            ? new Set(obj["expandedNodes"] as string[])
            : new Set<string>(),
        };
      }
    }
  } catch {
    // ignore
  }
  return { collapsed: false, expandedNodes: new Set<string>() };
}

function persist(state: SidebarState) {
  try {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        collapsed: state.collapsed,
        expandedNodes: [...state.expandedNodes],
      }),
    );
  } catch {
    // ignore
  }
}

export const useSidebarStore = create<SidebarState & SidebarActions>()(
  (set, get) => ({
    ...loadPersistedState(),
    toggleCollapsed: () => {
      const next = !get().collapsed;
      const state = { ...get(), collapsed: next };
      set({ collapsed: next });
      persist(state);
    },
    toggleNode: (key: string) => {
      const expanded = new Set(get().expandedNodes);
      if (expanded.has(key)) {
        expanded.delete(key);
      } else {
        expanded.add(key);
      }
      const state = { ...get(), expandedNodes: expanded };
      set({ expandedNodes: expanded });
      persist(state);
    },
    expandNode: (key: string) => {
      const expanded = new Set(get().expandedNodes);
      if (!expanded.has(key)) {
        expanded.add(key);
        const state = { ...get(), expandedNodes: expanded };
        set({ expandedNodes: expanded });
        persist(state);
      }
    },
  }),
);
