import { create } from "zustand";

/** The favicon index.html links, put back when a custom one is removed. */
export const DEFAULT_FAVICON = "/favicon.png";

/**
 * A branding value as `GET /settings/branding` sends it: the stored JSON,
 * already decoded, so a title arrives as the title itself. Parsing it a second
 * time threw on every saved title and dropped the logo. A value once written
 * JSON-encoded (`"\"Acme\""`) still reads as the string inside.
 */
export function brandingText(value: unknown): string | null {
  if (typeof value !== "string") return null;
  if (value.length >= 2 && value.startsWith('"') && value.endsWith('"')) {
    try {
      const inner: unknown = JSON.parse(value);
      if (typeof inner === "string") return inner;
    } catch {
      // Not JSON after all: the value is the text.
    }
  }
  return value;
}

interface BrandingState {
  appTitle: string;
  logoUrl: string | null;
  faviconUrl: string | null;
  loaded: boolean;
  setAppTitle: (title: string) => void;
  setLogoUrl: (url: string | null) => void;
  setFaviconUrl: (url: string | null) => void;
  loadFromBranding: (data: Record<string, unknown>) => void;
}

export const useBrandingStore = create<BrandingState>()((set, get) => ({
  appTitle: "Nexara",
  logoUrl: null,
  faviconUrl: null,
  loaded: false,

  setAppTitle: (title: string) => {
    set({ appTitle: title });
    document.title = title;
  },

  setLogoUrl: (url: string | null) => {
    set({ logoUrl: url });
  },

  // The tab's icon follows in useLoadBranding, which can read the file.
  setFaviconUrl: (url: string | null) => {
    set({ faviconUrl: url });
  },

  loadFromBranding: (data: Record<string, unknown>) => {
    const state: Partial<BrandingState> = { loaded: true };

    // The default when none is saved, so a title reset elsewhere goes here too;
    // document.title only on a change, leaving a page's own title alone.
    const title = brandingText(data["branding.app_title"]) || "Nexara";
    if (title !== get().appTitle) {
      state.appTitle = title;
      document.title = title;
    }

    // Set either way: a logo or favicon the server no longer reports was
    // removed elsewhere (another tab, another admin) and must go here too.
    state.logoUrl = brandingText(data["branding.logo_url"]);
    state.faviconUrl = brandingText(data["branding.favicon_url"]);

    set(state);
  },
}));
