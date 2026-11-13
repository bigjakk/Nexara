import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

const STORAGE_KEY = "nexara-sidebar";

// The store reads localStorage once, when its module loads, so each case
// loads a fresh copy.
async function loadWith(stored: object) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(stored));
  vi.resetModules();
  const { useSidebarStore } = await import("./sidebar-store");
  return useSidebarStore;
}

describe("sidebar-store", () => {
  beforeEach(() => {
    localStorage.clear();
  });
  afterEach(() => {
    localStorage.clear();
  });

  // `collapsed` was the old sidebar's flag; the rail has no collapsed state.
  it.each([
    {
      name: "a collapsed old sidebar becomes a hidden tree",
      stored: { collapsed: true, treeVisible: true },
      want: false,
    },
    {
      name: "an expanded one keeps the tree as it was",
      stored: { collapsed: false, treeVisible: true },
      want: true,
    },
    {
      name: "a hidden tree stays hidden",
      stored: { treeVisible: false },
      want: false,
    },
  ])("$name", async ({ stored, want }) => {
    const store = await loadWith(stored);
    expect(store.getState().treeVisible).toBe(want);
  });

  it("stops writing the old flag once anything is saved", async () => {
    const store = await loadWith({ collapsed: true });
    store.getState().setTreeVisible(true);
    expect(
      JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "{}") as object,
    ).not.toHaveProperty("collapsed");
  });
});
