import { create } from "zustand";

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

export const useBrandingStore = create<BrandingState>()((set) => ({
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

  setFaviconUrl: (url: string | null) => {
    set({ faviconUrl: url });
    if (url) {
      const link =
        document.querySelector<HTMLLinkElement>('link[rel="icon"]') ??
        document.createElement("link");
      link.rel = "icon";
      link.href = url;
      if (!link.parentNode) {
        document.head.appendChild(link);
      }
    }
  },

  loadFromBranding: (data: Record<string, unknown>) => {
    const state: Partial<BrandingState> = { loaded: true };

    if (typeof data["branding.app_title"] === "string") {
      try {
        const title = JSON.parse(data["branding.app_title"]) as string;
        state.appTitle = title;
        document.title = title;
      } catch {
        // ignore parse errors
      }
    }

    if (typeof data["branding.logo_url"] === "string") {
      try {
        state.logoUrl = JSON.parse(data["branding.logo_url"]) as string;
      } catch {
        // ignore
      }
    }

    if (typeof data["branding.favicon_url"] === "string") {
      try {
        const url = JSON.parse(data["branding.favicon_url"]) as string;
        state.faviconUrl = url;
        const link =
          document.querySelector<HTMLLinkElement>('link[rel="icon"]') ??
          document.createElement("link");
        link.rel = "icon";
        link.href = url;
        if (!link.parentNode) {
          document.head.appendChild(link);
        }
      } catch {
        // ignore
      }
    }

    set(state);
  },
}));
