import { create } from "zustand";

// Tracks infrastructure-health issue *types* the user has muted (e.g.
// "task_failed"). Unlike a dismissal — which hides one occurrence until it
// resolves — a mute permanently suppresses every issue of that type across all
// clusters until the user restores it. Persisted to localStorage.

interface HealthMuteState {
  mutedTypes: string[];
  mute: (type: string) => void;
  unmute: (type: string) => void;
  restoreAll: () => void;
}

const STORAGE_KEY = "nexara-health-muted-types";

function loadStored(): string[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw === null) return [];
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed)
      ? parsed.filter((x): x is string => typeof x === "string")
      : [];
  } catch {
    return [];
  }
}

function save(types: string[]): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(types));
  } catch {
    // ignore quota / private-mode errors
  }
}

export const useHealthMuteStore = create<HealthMuteState>()((set) => ({
  mutedTypes: loadStored(),
  mute: (type) => {
    set((s) => {
      if (s.mutedTypes.includes(type)) return s;
      const next = [...s.mutedTypes, type];
      save(next);
      return { mutedTypes: next };
    });
  },
  unmute: (type) => {
    set((s) => {
      if (!s.mutedTypes.includes(type)) return s;
      const next = s.mutedTypes.filter((t) => t !== type);
      save(next);
      return { mutedTypes: next };
    });
  },
  restoreAll: () => {
    set((s) => {
      if (s.mutedTypes.length === 0) return s;
      save([]);
      return { mutedTypes: [] };
    });
  },
}));
