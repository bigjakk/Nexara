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

  it("injects a <link rel='icon'> element when none exists", () => {
    useBrandingStore.getState().setFaviconUrl("https://example.com/fav.ico");
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    expect(link).not.toBeNull();
    expect(link?.href).toContain("fav.ico");
  });

  it("updates existing <link rel='icon'> href when element already present", () => {
    // Pre-create a link element.
    const link = document.createElement("link");
    link.rel = "icon";
    link.href = "https://example.com/old.ico";
    document.head.appendChild(link);

    useBrandingStore.getState().setFaviconUrl("https://example.com/new.ico");
    const found = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    expect(found?.href).toContain("new.ico");
  });

  it("does not inject a DOM element when url is null", () => {
    useBrandingStore.getState().setFaviconUrl(null);
    const link = document.querySelector('link[rel="icon"]');
    expect(link).toBeNull();
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

  it("parses a JSON-encoded app title from branding.app_title key", () => {
    useBrandingStore.getState().loadFromBranding({
      "branding.app_title": JSON.stringify("Acme Platform"),
    });
    expect(useBrandingStore.getState().appTitle).toBe("Acme Platform");
  });

  it("updates document.title when branding.app_title is provided", () => {
    useBrandingStore.getState().loadFromBranding({
      "branding.app_title": JSON.stringify("Acme Platform"),
    });
    expect(document.title).toBe("Acme Platform");
  });

  it("parses a JSON-encoded logo URL from branding.logo_url key", () => {
    useBrandingStore.getState().loadFromBranding({
      "branding.logo_url": JSON.stringify("https://example.com/logo.svg"),
    });
    expect(useBrandingStore.getState().logoUrl).toBe(
      "https://example.com/logo.svg",
    );
  });

  it("parses a JSON-encoded favicon URL from branding.favicon_url key", () => {
    useBrandingStore.getState().loadFromBranding({
      "branding.favicon_url": JSON.stringify(
        "https://example.com/favicon.ico",
      ),
    });
    expect(useBrandingStore.getState().faviconUrl).toBe(
      "https://example.com/favicon.ico",
    );
  });

  it("injects favicon DOM element when branding.favicon_url is provided", () => {
    useBrandingStore.getState().loadFromBranding({
      "branding.favicon_url": JSON.stringify(
        "https://example.com/favicon.ico",
      ),
    });
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    expect(link).not.toBeNull();
    expect(link?.href).toContain("favicon.ico");
  });

  it("loads all three branding fields together", () => {
    useBrandingStore.getState().loadFromBranding({
      "branding.app_title": JSON.stringify("Full Platform"),
      "branding.logo_url": JSON.stringify("https://example.com/logo.png"),
      "branding.favicon_url": JSON.stringify("https://example.com/fav.png"),
    });
    const state = useBrandingStore.getState();
    expect(state.appTitle).toBe("Full Platform");
    expect(state.logoUrl).toBe("https://example.com/logo.png");
    expect(state.faviconUrl).toBe("https://example.com/fav.png");
    expect(state.loaded).toBe(true);
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

  it("silently ignores malformed JSON in branding.app_title", () => {
    // Should not throw; store title should remain unchanged.
    expect(() => {
      useBrandingStore.getState().loadFromBranding({
        "branding.app_title": "{not-valid-json",
      });
    }).not.toThrow();
    expect(useBrandingStore.getState().appTitle).toBe("Nexara");
  });

  it("silently ignores malformed JSON in branding.logo_url", () => {
    expect(() => {
      useBrandingStore.getState().loadFromBranding({
        "branding.logo_url": "not-json-at-all",
      });
    }).not.toThrow();
    // JSON.parse("not-json-at-all") throws, so logoUrl stays null.
    expect(useBrandingStore.getState().logoUrl).toBeNull();
  });

  it("silently ignores malformed JSON in branding.favicon_url", () => {
    expect(() => {
      useBrandingStore.getState().loadFromBranding({
        "branding.favicon_url": "bad-json",
      });
    }).not.toThrow();
    expect(useBrandingStore.getState().faviconUrl).toBeNull();
  });

  it("sets loaded to true even when no branding keys are present", () => {
    useBrandingStore.getState().loadFromBranding({});
    expect(useBrandingStore.getState().loaded).toBe(true);
  });

  it("does not mutate appTitle when branding.app_title is absent", () => {
    useBrandingStore.getState().loadFromBranding({
      "branding.logo_url": JSON.stringify("https://example.com/logo.png"),
    });
    expect(useBrandingStore.getState().appTitle).toBe("Nexara");
  });
});
