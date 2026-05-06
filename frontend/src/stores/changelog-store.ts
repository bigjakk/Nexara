import { create } from "zustand";

interface ChangelogState {
  lastSeenVersion: string | null;
  setLastSeenVersion: (version: string) => void;
}

const STORAGE_KEY = "nexara-changelog-version";

function loadStored(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

export const useChangelogStore = create<ChangelogState>()((set) => ({
  lastSeenVersion: loadStored(),
  setLastSeenVersion: (version: string) => {
    try {
      localStorage.setItem(STORAGE_KEY, version);
    } catch {
      // ignore quota / private mode errors
    }
    set({ lastSeenVersion: version });
  },
}));
