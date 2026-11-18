import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

import { useSidebarStore } from "@/stores/sidebar-store";
import { useTreeVisibility } from "./useTreeVisibility";

function viewport(width: number) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches:
      query.includes("min-width: 768px") && query.includes("max-width: 1023px")
        ? width >= 768 && width <= 1023
        : query.includes("max-width: 767px") && width <= 767,
    media: query,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  }));
}

afterEach(() => {
  vi.unstubAllGlobals();
  useSidebarStore.setState({ treeVisible: true, treeShownNarrow: false });
});

// At 800 px the tree stayed open beside the page and left it 484 px.
describe("useTreeVisibility", () => {
  it.each([true, false])(
    "starts hidden at a narrow desktop width, and showing it there leaves the saved choice (%s) alone",
    (saved) => {
      viewport(800);
      useSidebarStore.setState({ treeVisible: saved });
      const { result } = renderHook(() => useTreeVisibility());
      expect(result.current.visible).toBe(false);
      act(() => {
        result.current.show();
      });
      expect(result.current.visible).toBe(true);
      expect(useSidebarStore.getState().treeVisible).toBe(saved);
    },
  );

  it("follows the saved choice at a wide one", () => {
    viewport(1440);
    useSidebarStore.setState({ treeVisible: true });
    const { result } = renderHook(() => useTreeVisibility());
    expect(result.current.visible).toBe(true);
    act(() => {
      result.current.hide();
    });
    expect(useSidebarStore.getState().treeVisible).toBe(false);
  });
});
