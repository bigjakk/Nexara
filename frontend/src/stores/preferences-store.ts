import { create } from "zustand";
import i18n from "@/lib/i18n";

export type ByteUnit = "binary" | "decimal";
/** The retired Date format value: saved by older versions, never read. */
export type DateFormat = "relative" | "iso" | "locale";
/**
 * How dates are written (Settings → Appearance). "auto", the default, is what
 * the app showed before the choice was honoured: relative in the few places
 * that were, the browser's locale everywhere else.
 */
export type DateDisplay = "auto" | "relative" | "iso" | "locale";
/** Where a console opens: the floating window, or a browser tab of its own. */
export type ConsoleOpenMode = "floating" | "tab";

export interface UserPreferences {
  byteUnit: ByteUnit;
  /** Retired: its stored default ("relative") was never honoured. */
  dateFormat: DateFormat;
  dateDisplay: DateDisplay;
  refreshInterval: number; // seconds, 0 = manual
  accentColor: string; // HSL string e.g. "240 5.9% 10%" or preset name
  language: string; // BCP 47 language code e.g. "en", "de", "fr"
  consoleOpenMode: ConsoleOpenMode;
}

interface PreferencesState {
  preferences: UserPreferences;
  loaded: boolean;
  setPreferences: (prefs: Partial<UserPreferences>) => void;
  loadFromJSON: (json: unknown) => void;
  toJSON: () => UserPreferences;
}

const STORAGE_KEY = "nexara-preferences";

const defaultPreferences: UserPreferences = {
  byteUnit: "binary",
  dateFormat: "relative",
  dateDisplay: "auto",
  refreshInterval: 30,
  accentColor: "default",
  language: "en",
  consoleOpenMode: "floating",
};

function loadStored(): UserPreferences {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<UserPreferences>;
      return { ...defaultPreferences, ...parsed };
    }
  } catch {
    // ignore
  }
  return { ...defaultPreferences };
}

export const usePreferencesStore = create<PreferencesState>()((set, get) => ({
  preferences: loadStored(),
  loaded: false,
  setPreferences: (prefs: Partial<UserPreferences>) => {
    const updated = { ...get().preferences, ...prefs };
    localStorage.setItem(STORAGE_KEY, JSON.stringify(updated));
    if (prefs.language) {
      localStorage.setItem("nexara-language", prefs.language);
      void i18n.changeLanguage(prefs.language);
    }
    set({ preferences: updated });
  },
  loadFromJSON: (json: unknown) => {
    if (json && typeof json === "object") {
      const prefs = json as Partial<UserPreferences>;
      const merged = { ...defaultPreferences, ...prefs };
      localStorage.setItem(STORAGE_KEY, JSON.stringify(merged));
      if (merged.language && merged.language !== i18n.language) {
        localStorage.setItem("nexara-language", merged.language);
        void i18n.changeLanguage(merged.language);
      }
      set({ preferences: merged, loaded: true });
    }
  },
  toJSON: () => get().preferences,
}));
