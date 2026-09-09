import { create } from "zustand";

export type TreePerspective = "hosts" | "storage" | "vms";

interface SidebarState {
  collapsed: boolean;
  treeVisible: boolean;
  expandedNodes: Set<string>;
  width: number;
  perspective: TreePerspective;
  /**
   * Whether the Favorites section above the tree is folded away.
   *
   * A field of its own rather than a key in expandedNodes, because that set
   * treats absence as collapsed and this section has to default to OPEN — a
   * list you curated yourself is useless if it starts hidden. Encoding that as
   * a "favorites-collapsed" member of a set named expandedNodes would invert
   * the set's meaning for one entry.
   */
  favoritesCollapsed: boolean;
}

interface SidebarActions {
  toggleCollapsed: () => void;
  setTreeVisible: (visible: boolean) => void;
  toggleNode: (key: string) => void;
  expandNode: (key: string) => void;
  setWidth: (width: number) => void;
  setPerspective: (perspective: TreePerspective) => void;
  toggleFavoritesCollapsed: () => void;
}

const STORAGE_KEY = "nexara-sidebar";

function loadPersistedState(): SidebarState {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed: unknown = JSON.parse(raw);
      if (parsed && typeof parsed === "object") {
        const obj = parsed as Record<string, unknown>;
        const persp = obj["perspective"];
        return {
          collapsed:
            typeof obj["collapsed"] === "boolean" ? obj["collapsed"] : false,
          treeVisible:
            typeof obj["treeVisible"] === "boolean" ? obj["treeVisible"] : true,
          expandedNodes: Array.isArray(obj["expandedNodes"])
            ? new Set(obj["expandedNodes"] as string[])
            : new Set<string>(),
          width: typeof obj["width"] === "number" ? obj["width"] : 240,
          perspective:
            persp === "storage" ? "storage" : persp === "vms" ? "vms" : "hosts",
          favoritesCollapsed: obj["favoritesCollapsed"] === true,
        };
      }
    }
  } catch {
    // ignore
  }
  return {
    collapsed: false,
    treeVisible: true,
    expandedNodes: new Set<string>(),
    width: 240,
    perspective: "vms",
    favoritesCollapsed: false,
  };
}

function persist(state: SidebarState) {
  try {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        collapsed: state.collapsed,
        treeVisible: state.treeVisible,
        expandedNodes: [...state.expandedNodes],
        width: state.width,
        perspective: state.perspective,
        favoritesCollapsed: state.favoritesCollapsed,
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
    setTreeVisible: (visible: boolean) => {
      const state = { ...get(), treeVisible: visible };
      set({ treeVisible: visible });
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
    setWidth: (width: number) => {
      const clamped = Math.min(480, Math.max(180, width));
      const state = { ...get(), width: clamped };
      set({ width: clamped });
      persist(state);
    },
    setPerspective: (perspective: TreePerspective) => {
      const state = { ...get(), perspective };
      set({ perspective });
      persist(state);
    },
    toggleFavoritesCollapsed: () => {
      const next = !get().favoritesCollapsed;
      const state = { ...get(), favoritesCollapsed: next };
      set({ favoritesCollapsed: next });
      persist(state);
    },
  }),
);
