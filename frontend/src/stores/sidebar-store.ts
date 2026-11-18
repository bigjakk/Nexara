import { create } from "zustand";

export type TreePerspective = "hosts" | "storage" | "vms";

interface SidebarState {
  /** Whether the inventory tree is shown beside the navigation rail. */
  treeVisible: boolean;
  /**
   * Whether the tree is shown at a narrow desktop width, where it starts
   * hidden whatever `treeVisible` says. Kept for this page load only.
   */
  treeShownNarrow: boolean;
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
  setTreeVisible: (visible: boolean) => void;
  setTreeShownNarrow: (shown: boolean) => void;
  toggleNode: (key: string) => void;
  expandNode: (key: string) => void;
  setWidth: (width: number) => void;
  setPerspective: (perspective: TreePerspective) => void;
  toggleFavoritesCollapsed: () => void;
}

const STORAGE_KEY = "nexara-sidebar";

/** The tree panel's width, held to what stays usable on either side of it. */
export function clampTreeWidth(width: number): number {
  return Math.min(480, Math.max(180, width));
}

function loadPersistedState(): Omit<SidebarState, "treeShownNarrow"> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed: unknown = JSON.parse(raw);
      if (parsed && typeof parsed === "object") {
        const obj = parsed as Record<string, unknown>;
        const persp = obj["perspective"];
        return {
          // `collapsed` is the old sidebar's flag. It asked for the least
          // chrome, which is now a hidden tree; it is never written again.
          treeVisible:
            obj["collapsed"] === true
              ? false
              : typeof obj["treeVisible"] === "boolean"
                ? obj["treeVisible"]
                : true,
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
    treeShownNarrow: false,
    setTreeVisible: (visible: boolean) => {
      const state = { ...get(), treeVisible: visible };
      set({ treeVisible: visible });
      persist(state);
    },
    setTreeShownNarrow: (shown: boolean) => {
      set({ treeShownNarrow: shown });
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
      const clamped = clampTreeWidth(width);
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
