import { describe, it, expect, afterEach, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useIsMobile } from "./useIsMobile";

function mockMatchMedia(matches: boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockImplementation((query: string) => ({
      matches,
      media: query,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
    })),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useIsMobile", () => {
  it("reports the breakpoint the app shell switches layouts on", () => {
    mockMatchMedia(true);
    expect(renderHook(() => useIsMobile()).result.current).toBe(true);
    mockMatchMedia(false);
    expect(renderHook(() => useIsMobile()).result.current).toBe(false);
  });

  it("queries below Tailwind's md, not at or above it", () => {
    const spy = vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
    }));
    vi.stubGlobal("matchMedia", spy);
    renderHook(() => useIsMobile());
    // 767 and not 768: the two are complements, and a hook that asked
    // `min-width: 768px` would return the opposite of its own name.
    expect(spy).toHaveBeenCalledWith("(max-width: 767px)");
  });

  // Every consumer is a layout decision, and a layout decision that throws
  // takes the whole page with it. Assume desktop instead: useColumnLayout in
  // particular would otherwise fail to render a table rather than render a
  // wide one.
  it("assumes desktop rather than throwing where matchMedia is absent", () => {
    vi.stubGlobal("matchMedia", undefined);
    expect(() => renderHook(() => useIsMobile())).not.toThrow();
    expect(renderHook(() => useIsMobile()).result.current).toBe(false);
  });
});
