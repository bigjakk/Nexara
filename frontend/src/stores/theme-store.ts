import { create } from "zustand";

type ThemeMode = "light" | "dark" | "system";

interface ThemeState {
  mode: ThemeMode;
  setMode: (mode: ThemeMode) => void;
}

const STORAGE_KEY = "proxdash-theme";

function getStoredMode(): ThemeMode {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw === "light" || raw === "dark" || raw === "system") return raw;
  } catch {
    // ignore
  }
  return "system";
}

function applyTheme(mode: ThemeMode) {
  const isDark =
    mode === "dark" ||
    (mode === "system" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", isDark);

  // Re-apply accent color when theme changes (light/dark have different accent values)
  reapplyAccentColor();
}

export const useThemeStore = create<ThemeState>()((set) => ({
  mode: getStoredMode(),
  setMode: (mode: ThemeMode) => {
    localStorage.setItem(STORAGE_KEY, mode);
    applyTheme(mode);
    set({ mode });
  },
}));

// Apply on load and listen for system preference changes.
applyTheme(getStoredMode());

const mq = window.matchMedia("(prefers-color-scheme: dark)");
mq.addEventListener("change", () => {
  if (useThemeStore.getState().mode === "system") {
    applyTheme("system");
  }
});

// Accent color definitions (shared with AppearancePage)
const accentColorMap: Record<string, { hsl: string; darkHsl: string }> = {
  blue: { hsl: "221 83% 53%", darkHsl: "217 91% 60%" },
  green: { hsl: "142 71% 45%", darkHsl: "142 71% 45%" },
  purple: { hsl: "262 83% 58%", darkHsl: "262 83% 58%" },
  orange: { hsl: "25 95% 53%", darkHsl: "25 95% 53%" },
  red: { hsl: "0 72% 51%", darkHsl: "0 72% 51%" },
  pink: { hsl: "330 81% 60%", darkHsl: "330 81% 60%" },
  teal: { hsl: "173 80% 40%", darkHsl: "173 80% 40%" },
  cyan: { hsl: "189 94% 43%", darkHsl: "189 94% 43%" },
  amber: { hsl: "38 92% 50%", darkHsl: "38 92% 50%" },
};

function reapplyAccentColor() {
  try {
    const raw = localStorage.getItem("proxdash-preferences");
    if (!raw) return;
    const prefs = JSON.parse(raw) as { accentColor?: string };
    if (!prefs.accentColor || prefs.accentColor === "default") {
      document.documentElement.style.removeProperty("--primary");
      document.documentElement.style.removeProperty("--ring");
      return;
    }
    const color = accentColorMap[prefs.accentColor];
    if (color) {
      const isDark = document.documentElement.classList.contains("dark");
      const hsl = isDark ? color.darkHsl : color.hsl;
      document.documentElement.style.setProperty("--primary", hsl);
      document.documentElement.style.setProperty("--ring", hsl);
    }
  } catch {
    // ignore
  }
}

// Apply stored accent color on initial load
reapplyAccentColor();

export { reapplyAccentColor };
