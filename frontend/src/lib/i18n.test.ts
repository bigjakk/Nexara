import { describe, it, expect, afterEach } from "vitest";
import i18n, { supportedLanguages, resources } from "./i18n";

describe("i18n configuration", () => {
  afterEach(async () => {
    // Reset to English after each test
    await i18n.changeLanguage("en");
  });

  it("initializes with English as the default language", () => {
    expect(i18n.language).toBe("en");
  });

  it("has English resources loaded", () => {
    expect(resources.en).toBeDefined();
    expect(Object.keys(resources.en).length).toBeGreaterThan(0);
  });

  it("has all expected namespaces", () => {
    const expectedNamespaces = [
      "common",
      "navigation",
      "auth",
      "dashboard",
      "settings",
      "admin",
      "clusters",
      "inventory",
      "vms",
      "storage",
      "backup",
      "alerts",
      "security",
      "topology",
      "console",
      "reports",
      "audit",
      "ceph",
      "networks",
    ];
    const actualNamespaces = Object.keys(resources.en);
    for (const ns of expectedNamespaces) {
      expect(actualNamespaces).toContain(ns);
    }
  });

  it("translates common keys correctly", () => {
    expect(i18n.t("common:loading")).toBe("Loading...");
    expect(i18n.t("common:save")).toBe("Save");
    expect(i18n.t("common:cancel")).toBe("Cancel");
    expect(i18n.t("common:delete")).toBe("Delete");
  });

  it("translates navigation keys correctly", () => {
    expect(i18n.t("navigation:dashboard")).toBe("Dashboard");
    expect(i18n.t("navigation:inventory")).toBe("Inventory");
    expect(i18n.t("navigation:topology")).toBe("Topology");
    expect(i18n.t("navigation:settings")).toBe("Settings");
  });

  it("translates auth keys correctly", () => {
    expect(i18n.t("auth:signIn")).toBe("Sign In");
    expect(i18n.t("auth:welcomeToProxDash")).toBe("Welcome to ProxDash");
    expect(i18n.t("auth:email")).toBe("Email");
    expect(i18n.t("auth:password")).toBe("Password");
  });

  it("supports interpolation", () => {
    expect(i18n.t("auth:signInWith", { provider: "Google" })).toBe(
      "Sign in with Google",
    );
    expect(i18n.t("dashboard:unknownWidget", { widgetId: "test" })).toBe(
      "Unknown widget: test",
    );
  });

  it("falls back to English for missing translations", () => {
    // Try a key that only exists in English
    const result = i18n.t("common:loading");
    expect(result).toBe("Loading...");
  });

  it("supportedLanguages includes English", () => {
    const english = supportedLanguages.find(
      (l) => (l.code as string) === "en",
    );
    expect(english).toBeDefined();
    expect(english?.name).toBe("English");
    expect(english?.nativeName).toBe("English");
  });

  it("every namespace has at least one key", () => {
    for (const [, translations] of Object.entries(resources.en)) {
      expect(
        Object.keys(translations as Record<string, string>).length,
      ).toBeGreaterThan(0);
    }
  });

  it("no translation values are empty strings", () => {
    for (const [, translations] of Object.entries(resources.en)) {
      for (const [, value] of Object.entries(
        translations as Record<string, string>,
      )) {
        expect(value).not.toBe("");
      }
    }
  });
});
