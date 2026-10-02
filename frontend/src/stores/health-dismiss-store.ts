import { create } from "zustand";

// Tracks infrastructure-health issues the user has dismissed from the header
// pill. Issues are derived live from cluster state on every poll, so a dismissal
// must be remembered (persisted) to actually stick — and forgotten once the
// underlying condition resolves, so a resolved-then-recurring issue surfaces
// again as new. Dismissals are keyed by a "signature" that encodes the issue's
// identity *and* state (cluster, kind, severity, reasons); any meaningful change
// produces a new signature and re-surfaces the issue.

interface HealthDismissState {
  dismissed: string[];
  /** Dismiss an issue by signature. */
  dismiss: (sig: string) => void;
  /** Un-dismiss everything. */
  restoreAll: () => void;
  /** Drop dismissals whose issue is no longer active, so recurrences re-show. */
  syncActive: (activeSigs: string[]) => void;
  /**
   * The signed-in session ended: drop every dismissal, in memory AND in storage.
   * Unlike restoreAll it does not take this tab's list for what is stored — it
   * was read once, at load, and another tab may have written dismissals since,
   * which carry a cluster id, a target and the issue's detail text.
   */
  resetSession: () => void;
}

const STORAGE_KEY = "nexara-health-dismissed";

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

function save(sigs: string[]): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(sigs));
  } catch {
    // ignore quota / private-mode errors
  }
}

export const useHealthDismissStore = create<HealthDismissState>()((set) => ({
  dismissed: loadStored(),
  dismiss: (sig) => {
    set((s) => {
      if (s.dismissed.includes(sig)) return s;
      const next = [...s.dismissed, sig];
      save(next);
      return { dismissed: next };
    });
  },
  restoreAll: () => {
    set((s) => {
      if (s.dismissed.length === 0) return s;
      save([]);
      return { dismissed: [] };
    });
  },
  syncActive: (activeSigs) => {
    set((s) => {
      const active = new Set(activeSigs);
      const next = s.dismissed.filter((sig) => active.has(sig));
      if (next.length === s.dismissed.length) return s;
      save(next);
      return { dismissed: next };
    });
  },
  resetSession: () => {
    save([]);
    set((s) => (s.dismissed.length === 0 ? s : { dismissed: [] }));
  },
}));
