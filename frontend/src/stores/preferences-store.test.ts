import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { usePreferencesStore } from "./preferences-store";
import type { UserPreferences } from "./preferences-store";

const STORAGE_KEY = "proxdash-preferences";

const defaultPreferences: UserPreferences = {
  byteUnit: "binary",
  dateFormat: "relative",
  refreshInterval: 30,
  accentColor: "default",
};

describe("preferences-store — defaults", () => {
  beforeEach(() => {
    localStorage.clear();
    // Reset Zustand store to a clean state derived from empty localStorage.
    usePreferencesStore.setState({
      preferences: { ...defaultPreferences },
      loaded: false,
    });
  });

  afterEach(() => {
    localStorage.clear();
  });

  it("has the correct default byteUnit", () => {
    const { preferences } = usePreferencesStore.getState();
    expect(preferences.byteUnit).toBe("binary");
  });

  it("has the correct default dateFormat", () => {
    const { preferences } = usePreferencesStore.getState();
    expect(preferences.dateFormat).toBe("relative");
  });

  it("has the correct default refreshInterval", () => {
    const { preferences } = usePreferencesStore.getState();
    expect(preferences.refreshInterval).toBe(30);
  });

  it("has the correct default accentColor", () => {
    const { preferences } = usePreferencesStore.getState();
    expect(preferences.accentColor).toBe("default");
  });

  it("loaded flag is false by default", () => {
    const { loaded } = usePreferencesStore.getState();
    expect(loaded).toBe(false);
  });
});

describe("preferences-store — setPreferences", () => {
  beforeEach(() => {
    localStorage.clear();
    usePreferencesStore.setState({
      preferences: { ...defaultPreferences },
      loaded: false,
    });
  });

  afterEach(() => {
    localStorage.clear();
  });

  it("updates a single preference field", () => {
    usePreferencesStore.getState().setPreferences({ byteUnit: "decimal" });
    expect(usePreferencesStore.getState().preferences.byteUnit).toBe("decimal");
  });

  it("preserves other fields when updating one field", () => {
    usePreferencesStore.getState().setPreferences({ dateFormat: "iso" });
    const { preferences } = usePreferencesStore.getState();
    expect(preferences.dateFormat).toBe("iso");
    expect(preferences.byteUnit).toBe("binary");
    expect(preferences.refreshInterval).toBe(30);
    expect(preferences.accentColor).toBe("default");
  });

  it("updates multiple fields at once", () => {
    usePreferencesStore
      .getState()
      .setPreferences({ byteUnit: "decimal", refreshInterval: 60 });
    const { preferences } = usePreferencesStore.getState();
    expect(preferences.byteUnit).toBe("decimal");
    expect(preferences.refreshInterval).toBe(60);
  });

  it("persists changes to localStorage", () => {
    usePreferencesStore.getState().setPreferences({ accentColor: "blue" });
    const stored = localStorage.getItem(STORAGE_KEY);
    expect(stored).not.toBeNull();
    const parsed = JSON.parse(stored ?? "{}") as Partial<UserPreferences>;
    expect(parsed.accentColor).toBe("blue");
  });

  it("persists all preference fields to localStorage", () => {
    usePreferencesStore.getState().setPreferences({
      byteUnit: "decimal",
      dateFormat: "locale",
      refreshInterval: 0,
      accentColor: "purple",
    });
    const stored = localStorage.getItem(STORAGE_KEY);
    expect(stored).not.toBeNull();
    const parsed = JSON.parse(stored ?? "{}") as UserPreferences;
    expect(parsed.byteUnit).toBe("decimal");
    expect(parsed.dateFormat).toBe("locale");
    expect(parsed.refreshInterval).toBe(0);
    expect(parsed.accentColor).toBe("purple");
  });

  it("can set refreshInterval to 0 (manual refresh)", () => {
    usePreferencesStore.getState().setPreferences({ refreshInterval: 0 });
    expect(usePreferencesStore.getState().preferences.refreshInterval).toBe(0);
  });
});

describe("preferences-store — loadFromJSON", () => {
  beforeEach(() => {
    localStorage.clear();
    usePreferencesStore.setState({
      preferences: { ...defaultPreferences },
      loaded: false,
    });
  });

  afterEach(() => {
    localStorage.clear();
  });

  it("merges provided values with defaults", () => {
    usePreferencesStore
      .getState()
      .loadFromJSON({ byteUnit: "decimal", dateFormat: "iso" });
    const { preferences } = usePreferencesStore.getState();
    expect(preferences.byteUnit).toBe("decimal");
    expect(preferences.dateFormat).toBe("iso");
    // Fields not in the JSON retain default values.
    expect(preferences.refreshInterval).toBe(30);
    expect(preferences.accentColor).toBe("default");
  });

  it("sets loaded flag to true after loading", () => {
    usePreferencesStore
      .getState()
      .loadFromJSON({ byteUnit: "decimal" });
    expect(usePreferencesStore.getState().loaded).toBe(true);
  });

  it("persists merged result to localStorage", () => {
    usePreferencesStore
      .getState()
      .loadFromJSON({ accentColor: "green", refreshInterval: 120 });
    const stored = localStorage.getItem(STORAGE_KEY);
    expect(stored).not.toBeNull();
    const parsed = JSON.parse(stored ?? "{}") as UserPreferences;
    expect(parsed.accentColor).toBe("green");
    expect(parsed.refreshInterval).toBe(120);
  });

  it("defaults are not overwritten when JSON has missing keys", () => {
    usePreferencesStore.getState().loadFromJSON({});
    const { preferences } = usePreferencesStore.getState();
    expect(preferences).toEqual(defaultPreferences);
  });

  it("ignores calls with non-object values", () => {
    const before = { ...usePreferencesStore.getState().preferences };
    usePreferencesStore.getState().loadFromJSON(null);
    const after = usePreferencesStore.getState().preferences;
    expect(after).toEqual(before);
    // loaded should stay false since the call was ignored
    expect(usePreferencesStore.getState().loaded).toBe(false);
  });

  it("ignores calls with a string value", () => {
    const before = { ...usePreferencesStore.getState().preferences };
    usePreferencesStore.getState().loadFromJSON("not-an-object");
    expect(usePreferencesStore.getState().preferences).toEqual(before);
  });

  it("can load all four preference fields at once", () => {
    const full: UserPreferences = {
      byteUnit: "decimal",
      dateFormat: "locale",
      refreshInterval: 5,
      accentColor: "red",
    };
    usePreferencesStore.getState().loadFromJSON(full);
    expect(usePreferencesStore.getState().preferences).toEqual(full);
  });
});

describe("preferences-store — toJSON", () => {
  beforeEach(() => {
    localStorage.clear();
    usePreferencesStore.setState({
      preferences: { ...defaultPreferences },
      loaded: false,
    });
  });

  afterEach(() => {
    localStorage.clear();
  });

  it("returns the current preferences object", () => {
    const result = usePreferencesStore.getState().toJSON();
    expect(result).toEqual(defaultPreferences);
  });

  it("reflects updates made via setPreferences", () => {
    usePreferencesStore
      .getState()
      .setPreferences({ byteUnit: "decimal", accentColor: "teal" });
    const result = usePreferencesStore.getState().toJSON();
    expect(result.byteUnit).toBe("decimal");
    expect(result.accentColor).toBe("teal");
    expect(result.dateFormat).toBe("relative");
    expect(result.refreshInterval).toBe(30);
  });

  it("returns a snapshot consistent with current store state", () => {
    const { preferences } = usePreferencesStore.getState();
    const json = usePreferencesStore.getState().toJSON();
    expect(json).toEqual(preferences);
  });

  it("returned object is the preferences object (same reference)", () => {
    // toJSON() returns get().preferences — same reference.
    const state = usePreferencesStore.getState();
    const json = state.toJSON();
    expect(json).toBe(state.preferences);
  });
});
