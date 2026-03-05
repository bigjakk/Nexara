import { create } from "zustand";

interface TaskLogState {
  panelOpen: boolean;
  panelHeight: number;
}

interface TaskLogActions {
  setPanelOpen: (open: boolean) => void;
  setPanelHeight: (height: number) => void;
}

const STORAGE_KEY = "proxdash-task-log";

function loadPersistedState(): Pick<TaskLogState, "panelOpen" | "panelHeight"> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed: unknown = JSON.parse(raw);
      if (parsed && typeof parsed === "object" && "panelOpen" in parsed) {
        const obj = parsed as Record<string, unknown>;
        return {
          panelOpen: typeof obj["panelOpen"] === "boolean" ? obj["panelOpen"] : false,
          panelHeight: typeof obj["panelHeight"] === "number" ? obj["panelHeight"] : 200,
        };
      }
    }
  } catch {
    // ignore
  }
  return { panelOpen: false, panelHeight: 200 };
}

function persist(state: Pick<TaskLogState, "panelOpen" | "panelHeight">) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch {
    // ignore
  }
}

export const useTaskLogStore = create<TaskLogState & TaskLogActions>()(
  (set, get) => ({
    ...loadPersistedState(),
    setPanelOpen: (open: boolean) => {
      set({ panelOpen: open });
      persist({ panelOpen: open, panelHeight: get().panelHeight });
    },
    setPanelHeight: (height: number) => {
      const clamped = Math.max(100, Math.min(height, window.innerHeight * 0.5));
      set({ panelHeight: clamped });
      persist({ panelOpen: get().panelOpen, panelHeight: clamped });
    },
  }),
);
