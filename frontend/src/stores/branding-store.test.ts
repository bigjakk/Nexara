import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { useBrandingStore } from "./branding-store";

// jsdom provides a minimal document; we reset relevant DOM state between tests.
function resetFavicon() {
  const existing = document.querySelector('link[rel="icon"]');
  if (existing) {
    existing.remove();
  }
}

describe("branding-store — default values", () => {
  beforeEach(() => {
    resetFavicon();
    useBrandingStore.setState({
      appTitle: "Nexara",
      logoUrl: null,
      faviconUrl: null,
      loaded: false,
    });
    document.title = "Nexara";
  });

  it("default appTitle is 'Nexara'", () => {
    expect(useBrandingStore.getState().appTitle).toBe("Nexara");
  });

  it("default logoUrl is null", () => {
    expect(useBrandingStore.getState().logoUrl).toBeNull();
  });

  it("default faviconUrl is null", () => {
    expect(useBrandingStore.getState().faviconUrl).toBeNull();
  });

  it("loaded is false by default", () => {
    expect(useBrandingStore.getState().loaded).toBe(false);
  });
});

describe("branding-store — setAppTitle", () => {
  beforeEach(() => {
    resetFavicon();
    useBrandingStore.setState({
      appTitle: "Nexara",
      logoUrl: null,
      faviconUrl: null,
      loaded: false,
    });
    document.title = "Nexara";
  });

  it("updates appTitle in the store", () => {
    useBrandingStore.getState().setAppTitle("My Platform");
    expect(useBrandingStore.getState().appTitle).toBe("My Platform");
  });

  it("updates document.title", () => {
    useBrandingStore.getState().setAppTitle("Infra Hub");
    expect(document.title).toBe("Infra Hub");
  });

  it("handles empty string title", () => {
    useBrandingStore.getState().setAppTitle("");
    expect(useBrandingStore.getState().appTitle).toBe("");
    expect(document.title).toBe("");
  });

  it("does not affect logoUrl or faviconUrl", () => {
    useBrandingStore.getState().setAppTitle("New Title");
    expect(useBrandingStore.getState().logoUrl).toBeNull();
    expect(useBrandingStore.getState().faviconUrl).toBeNull();
  });
});

describe("branding-store — setLogoUrl", () => {
  beforeEach(() => {
    resetFavicon();
    useBrandingStore.setState({
      appTitle: "Nexara",
      logoUrl: null,
      faviconUrl: null,
      loaded: false,
    });
  });

  it("updates logoUrl in the store", () => {
    useBrandingStore.getState().setLogoUrl("https://example.com/logo.png");
    expect(useBrandingStore.getState().logoUrl).toBe(
      "https://example.com/logo.png",
    );
  });

  it("can set logoUrl back to null", () => {
    useBrandingStore.getState().setLogoUrl("https://example.com/logo.png");
    useBrandingStore.getState().setLogoUrl(null);
    expect(useBrandingStore.getState().logoUrl).toBeNull();
  });

  it("does not affect appTitle or faviconUrl", () => {
    useBrandingStore.getState().setLogoUrl("https://example.com/logo.png");
    expect(useBrandingStore.getState().appTitle).toBe("Nexara");
    expect(useBrandingStore.getState().faviconUrl).toBeNull();
  });
});

describe("branding-store — setFaviconUrl", () => {
  beforeEach(() => {
    resetFavicon();
    useBrandingStore.setState({
      appTitle: "Nexara",
      logoUrl: null,
      faviconUrl: null,
      loaded: false,
    });
  });

  afterEach(() => {
    resetFavicon();
  });

  it("updates faviconUrl in the store", () => {
    useBrandingStore.getState().setFaviconUrl("https://example.com/fav.ico");
    expect(useBrandingStore.getState().faviconUrl).toBe(
      "https://example.com/fav.ico",
    );
  });

  it("stores null faviconUrl in the store", () => {
    useBrandingStore.getState().setFaviconUrl(null);
    expect(useBrandingStore.getState().faviconUrl).toBeNull();
  });
});

describe("branding-store — loadFromBranding", () => {
  beforeEach(() => {
    resetFavicon();
    useBrandingStore.setState({
      appTitle: "Nexara",
      logoUrl: null,
      faviconUrl: null,
      loaded: false,
    });
    document.title = "Nexara";
  });

  afterEach(() => {
    resetFavicon();
    vi.restoreAllMocks();
  });

  it("sets loaded to true", () => {
    useBrandingStore.getState().loadFromBranding({});
    expect(useBrandingStore.getState().loaded).toBe(true);
  });

  // The server sends each value decoded (`map[string]json.RawMessage`), so a
  // title arrives as the title; parsing it again threw on every saved title.
  it.each([
    ["the plain value the server sends", "Acme Platform"],
    ["a value once stored JSON-encoded", JSON.stringify("Acme Platform")],
  ])("reads the title from %s", (_label, value) => {
    useBrandingStore.getState().loadFromBranding({
      "branding.app_title": value,
    });
    expect(useBrandingStore.getState().appTitle).toBe("Acme Platform");
    expect(document.title).toBe("Acme Platform");
  });

  it("loads the logo and favicon URLs as the server sends them", () => {
    useBrandingStore.getState().loadFromBranding({
      "branding.logo_url": "/api/v1/settings/branding/logo-file",
      "branding.favicon_url": "/api/v1/settings/branding/favicon-file",
    });
    const state = useBrandingStore.getState();
    expect(state.logoUrl).toBe("/api/v1/settings/branding/logo-file");
    expect(state.faviconUrl).toBe("/api/v1/settings/branding/favicon-file");
  });

  // Reset elsewhere (another tab, another admin), each stayed here for good.
  it("puts back the title, logo and favicon the server no longer reports", () => {
    useBrandingStore.setState({
      appTitle: "Acme Platform",
      logoUrl: "/api/v1/settings/branding/logo-file",
      faviconUrl: "/api/v1/settings/branding/favicon-file",
    });
    document.title = "Acme Platform";
    useBrandingStore.getState().loadFromBranding({});
    const state = useBrandingStore.getState();
    expect(state.appTitle).toBe("Nexara");
    expect(document.title).toBe("Nexara");
    expect(state.logoUrl).toBeNull();
    expect(state.faviconUrl).toBeNull();
  });

  it("ignores non-string branding.app_title values", () => {
    useBrandingStore.getState().loadFromBranding({
      "branding.app_title": 42,
    });
    expect(useBrandingStore.getState().appTitle).toBe("Nexara");
  });

  it("ignores non-string branding.logo_url values", () => {
    useBrandingStore.getState().loadFromBranding({
      "branding.logo_url": true,
    });
    expect(useBrandingStore.getState().logoUrl).toBeNull();
  });

  it("ignores non-string branding.favicon_url values", () => {
    useBrandingStore.getState().loadFromBranding({
      "branding.favicon_url": null,
    });
    expect(useBrandingStore.getState().faviconUrl).toBeNull();
  });

  it("sets loaded to true even when no branding keys are present", () => {
    useBrandingStore.getState().loadFromBranding({});
    expect(useBrandingStore.getState().loaded).toBe(true);
  });
});
